package main

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

type Ratio string

const (
	Ratio16_9  Ratio = "16:9"
	Ratio9_16  Ratio = "9:16"
	RatioOther Ratio = "other"
)

type VideoStreamRes struct {
	Streams []struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	} `json:"streams"`
}

func getVideoAspectRatio(filePath string) (string, error) {
	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)
	stdout := bytes.Buffer{}
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", err
	}

	res := VideoStreamRes{}
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		return "", err
	}

	if len(res.Streams) < 1 {
		return "", errors.New("invalid streams length")
	}

	width := res.Streams[0].Width
	height := res.Streams[0].Height

	// floor to integer and call it a day 🫠
	// multiply to 100 to get 2 digits precision
	ratio := width * 100 / height
	switch ratio {
	case 177:
		return string(Ratio16_9), nil
	case 56:
		return string(Ratio9_16), nil
	default:
		return string(RatioOther), nil
	}
}

func processVideoForFastStart(filePath string) (string, error) {
	newPath := filePath + ".processing"
	cmd := exec.Command(
		"ffmpeg",
		"-i",
		filePath,
		"-c",
		"copy",
		"-movflags",
		"faststart",
		"-f",
		"mp4",
		newPath,
	)
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return newPath, nil
}

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	const videoSizeLimit = 1 << 30
	r.Body = http.MaxBytesReader(w, r.Body, videoSizeLimit)

	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	fmt.Println("uploading video", videoID, "by user", userID)

	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respondWithError(w, http.StatusNotFound, "No video with payload ID", err)
			return
		}
		respondWithError(w, http.StatusInternalServerError, "Can't retrive video", err)
		return
	}
	if video.UserID.String() != userID.String() {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	err = r.ParseMultipartForm(videoSizeLimit)
	if err != nil {
		var limitErr *http.MaxBytesError
		if errors.As(err, &limitErr) {
			respondWithError(w, http.StatusRequestEntityTooLarge, "Exceed file limit", err)
			return
		}
		respondWithError(w, http.StatusInternalServerError, "Couldn't parse form", err)
		return
	}

	file, headers, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Missing video", err)
		return
	}

	defer file.Close()

	mediatype, _, err := mime.ParseMediaType(headers.Header.Get("Content-Type"))
	if err != nil || mediatype != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Invalid content type", err)
		return
	}

	tempFile, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Coudn't create temp file", err)
		return
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()
	_, err = io.Copy(tempFile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't copy video to temp file", err)
		return
	}

	ratio, err := getVideoAspectRatio(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Coudn't get video aspect ratio", err)
		return
	}

	prefix := "other"
	switch ratio {
	case string(Ratio16_9):
		prefix = "landscape"
	case string(Ratio9_16):
		prefix = "portrait"
	}

	processedVidPath, err := processVideoForFastStart(tempFile.Name())
	if err != nil {
		fmt.Println(err)
		respondWithError(w, http.StatusInternalServerError, "Coudn't process video", err)
		return
	}

	processedFile, err := os.Open(processedVidPath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Coudn't open processed file", err)
		return
	}

	fileNameBase := make([]byte, 32)
	rand.Read(fileNameBase)
	fileNameB64 := base64.URLEncoding.EncodeToString(fileNameBase)
	fileName := fmt.Sprintf("%v/%v.mp4", prefix, fileNameB64)

	_, err = cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &fileName,
		Body:        processedFile,
		ContentType: &mediatype,
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to put video to storage", err)
		return
	}

	vidURL := fmt.Sprintf(
		"https://%v.s3.%v.amazonaws.com/%v",
		cfg.s3Bucket,
		cfg.s3Region,
		fileName,
	)
	video.VideoURL = &vidURL

	err = cfg.db.UpdateVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to update video metadata", err)
		return
	}

	respondWithJSON(w, http.StatusOK, video)
}
