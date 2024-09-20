package main

import (
	"context"
	"os"
	"time"

	"github.com/slack-go/slack"
)

var (
	// Slack Bot Token
	slackToken = "xoxb-xxxxxxxxxxxx-xxxxxxxxxxxx-xxxxxxxxxxxxxxxxxxxxxxxx"
	// Slack Channel ID to upload file
	chID = "CXXXXXXXX"
	// Sample image file path
	imageFilePath = "slack_icon.png"
)

func main() {
	ctx := context.Background()

	// load sample file and create io.Reader
	image, err := os.Open(imageFilePath)
	if err != nil {
		panic(err)
	}

	// post file using As-Is method
	_, err = UploadFile_AsIs(ctx, slackToken, chID, image)
	if err != nil {
		panic(err)
	}

	time.Sleep(5 * time.Second)

	// load sample file and create io.Reader
	image, err = os.Open(imageFilePath)
	if err != nil {
		panic(err)
	}

	// post file using To-Be method
	_, err = UploadFile_ToBe(ctx, slackToken, chID, image)
	if err != nil {
		panic(err)
	}
}

func UploadFile_AsIs(ctx context.Context, slackToken string, chID string, file *os.File) (*slack.File, error) {
	api := slack.New(slackToken)

	f, err := api.UploadFileContext(
		ctx,
		slack.FileUploadParameters{
			Reader:   file,
			Filename: "upload file name",
			Channels: []string{chID},
			Title:    "upload file title",
		})
	return f, err
}

func UploadFile_ToBe(ctx context.Context, slackToken string, chID string, file *os.File) (*slack.FileSummary, error) {
	api := slack.New(slackToken)

	// get size of file
	fileStat, err := file.Stat()
	if err != nil {
		panic(err)
	}
	size := fileStat.Size()

	f, err := api.UploadFileV2Context(ctx, slack.UploadFileV2Parameters{
		FileSize: int(size),
		Reader:   file,
		Filename: "upload file name",
		Title:    "upload file title",
		Channel:  chID,
	})
	return f, err
}
