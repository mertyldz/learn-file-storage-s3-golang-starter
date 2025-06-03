package main

import (
	"bytes"
	"context"
	"io"
	"mime"
	"net/http"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {

	videoIdStr := r.PathValue("videoID")
	videoId, err := uuid.Parse(videoIdStr)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Video id is not valid", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Auth token is not found", err)
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	videoDb, err := cfg.db.GetVideo(videoId)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Video is not found", err)
		return
	}
	if videoDb.UserID != userID {
		respondWithError(w, http.StatusForbidden, "This video does not belong to the user", err)
		return
	}

	const maxMemory = 10 << 30 // 1 GB
	r.ParseMultipartForm(maxMemory)
	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	defer file.Close()

	mediaType, _, err := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid Content-Type", err)
		return
	}
	if mediaType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Invalid file type", nil)
		return
	}

	tempMp4, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error while creating temporary file", err)
		return
	}
	defer os.Remove(tempMp4.Name())
	defer tempMp4.Close()

	if _, err = io.Copy(tempMp4, file); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error while saving video to temp file", err)
		return
	}
	tempMp4.Seek(0, io.SeekStart)
	processedMp4, err := processVideoForFastStart(tempMp4.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error while processing mp4", err)
		return
	}
	defer os.Remove(processedMp4)
	processedFile, err := os.ReadFile(processedMp4)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error while reading mp4", err)
		return
	}
	processedReader := bytes.NewReader(processedFile)

	mp4Key, err := createRandomPath()
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error while creating mp4 key", err)
		return
	}
	prefix := getVideoAspectType(tempMp4.Name())
	mp4KeyWithPrefix := prefix + "/" + mp4Key

	_, err = cfg.s3Client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:      aws.String(cfg.s3Bucket),
		Body:        processedReader,
		Key:         aws.String(mp4KeyWithPrefix),
		ContentType: aws.String(mediaType),
	})

	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error while uploading the mp4", err)
		return
	}

	s3VideoUrl := cfg.getObjectURL(mp4KeyWithPrefix)
	videoDb.VideoURL = &s3VideoUrl
	err = cfg.db.UpdateVideo(videoDb)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error while updating video record", err)
		return
	}
}
