package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

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

	_, err = tempFile.Seek(0, io.SeekStart)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Internal server error", err)
		return
	}

	fileNameBase := make([]byte, 32)
	rand.Read(fileNameBase)
	fileNameB64 := base64.URLEncoding.EncodeToString(fileNameBase)
	fileName := fmt.Sprintf("%v.mp4", fileNameB64)

	_, err = cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &fileName,
		Body:        tempFile,
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
