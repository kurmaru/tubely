package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
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

	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	// TODO: implement the upload here
	const maxMemory = 10 << 20
	r.ParseMultipartForm(maxMemory)

	file, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Bad request", err)
		return
	}
	defer file.Close()
	contentType := header.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Can't parse content type", err)
		return
	}
	if mediaType != "image/jpeg" && mediaType != "image/png" {
		respondWithError(w, http.StatusBadRequest, "Invalid file type", err)
		return
	}

	dbVid, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Coudn't find video", err)
		return
	}
	if dbVid.ID.String() == uuid.Nil.String() {
		respondWithError(w, http.StatusNotFound, "Video doesn't exist", err)
		return
	}
	if dbVid.UserID.String() != userID.String() {
		respondWithError(w, http.StatusUnauthorized, "User is not author", err)
		return
	}

	mediaTypeParts := strings.Split(mediaType, "/")
	fileExt := mediaTypeParts[1]

	fileNameBase := make([]byte, 32)
	rand.Read(fileNameBase)
	fileNameB64 := base64.URLEncoding.EncodeToString(fileNameBase)

	fileName := fmt.Sprintf("%v.%v", fileNameB64, fileExt)
	path := filepath.Join(cfg.assetsRoot, fileName)
	fileFS, err := os.Create(path)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to create file", err)
		return
	}
	defer fileFS.Close()

	_, err = io.Copy(fileFS, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to update file", err)
		return
	}

	thumbnailURL := fmt.Sprintf("http://localhost:%v/assets/%v", cfg.port, fileName)
	dbVid.ThumbnailURL = &thumbnailURL
	err = cfg.db.UpdateVideo(dbVid)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Coudn't update video", err)
		return
	}

	respondWithJSON(w, http.StatusOK, dbVid)
}
