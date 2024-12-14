package main

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"os"
)

const defaultPort = "8080"

// handler - リクエストをそのままレスポンスに書き込むHTTPハンドラ
func handler(w http.ResponseWriter, r *http.Request) {
	// リクエストをログにdump
	dump, err := httputil.DumpRequest(r, true)
	if err != nil {
		log.Printf("httputil.DumpRequest error - %+v", err)
	} else {
		log.Printf("DumpRequest\n%s", string(dump))
	}

	// リクエストをそのままレスポンスに書き込む
	fmt.Fprintf(w, "%s %s %s\n", r.Method, r.URL, r.Proto)
	r.Write(w)
}

func main() {
	// get port from environment variable "PORT", or default to 8089
	port, exists := os.LookupEnv("PORT")
	if !exists {
		port = defaultPort
	}

	http.HandleFunc("/", handler)
	log.Printf("Server started on :%s", port)
	err := http.ListenAndServe(":"+port, nil)

	if err != nil {
		log.Fatalf("ListenAndServe: %v", err)
	}
}
