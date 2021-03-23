package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
)

var bgctx = context.Background()

func printErr(a ...interface{}) {
	fmt.Fprintln(os.Stderr, a...)
}

func main() {
	flag.Parse()
	if flag.NArg() != 2 {
		printErr("usage:", os.Args[0], "<file> <server_url>")
		return
	}
	filePath := flag.Arg(0)
	serverURL := flag.Arg(1)

	f, err := os.Open(filePath)
	if err != nil {
		printErr(err)
		return
	}
	defer f.Close()

	uploader := newFileUploader(http.DefaultClient)
	uploadURL, err := uploader.requestUploadURL(bgctx, serverURL)
	if err != nil {
		printErr(err)
		return
	}
	fmt.Println("got upload URL:", uploadURL)
	err = uploader.uploadFile(bgctx, uploadURL, f)
	if err != nil {
		printErr(err)
		return
	}
	fmt.Println("file uploaded successfully")
}
