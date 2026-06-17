package main

import (
	"fmt"
	"io"
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
	mediaType := header.Header.Get("Content-Type")

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
	fileExt := ""
	switch len(mediaTypeParts) {
	case 1:
		fileExt = mediaTypeParts[0]
	case 2:
		fileExt = mediaTypeParts[1]
	}
	if fileExt == "" {
		respondWithError(w, http.StatusBadRequest, "Content-Type invalid", nil)
		return
	}

	fileName := fmt.Sprintf("%v.%v", videoID.String(), fileExt)
	path := filepath.Join(cfg.assetsRoot, fileName)
	fileFS, err := os.Create(path)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to create file", err)
		return
	}
	_, err = io.Copy(fileFS, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to update file", err)
		return
	}

	thumbnailURL := fmt.Sprintf("http://localhost:%v/assets/%v.%v", cfg.port, videoID.String(), fileExt)
	dbVid.ThumbnailURL = &thumbnailURL
	err = cfg.db.UpdateVideo(dbVid)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Coudn't update video", err)
		return
	}

	respondWithJSON(w, http.StatusOK, dbVid)
}
