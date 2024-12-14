package main

import (
	"log"
	"net/http"
	"net/http/httputil"
)

type LoggingInterceptor struct {
	Transport http.RoundTripper
}

func (i *LoggingInterceptor) RoundTrip(req *http.Request) (*http.Response, error) {

	dump, err := httputil.DumpRequest(req, true)
	if err != nil {
		log.Printf("DumpRequest error - %+v", err)
		// DO NOT return when error occurs because it's not critical
	} else {
		log.Printf("DumpRequest = %s", string(dump))
	}

	resp, err := i.Transport.RoundTrip(req)
	if err != nil {
		// logs are not printed here because log is assumed to be printed in caller function
		return resp, err
	}

	dump, err = httputil.DumpResponse(resp, true)
	if err != nil {
		log.Printf("DumpResponse error - %+v", err)
		return resp, err
	}

	log.Printf("DumpResponse = %s", string(dump))
	return resp, err
}

func main() {
	cli := &http.Client{
		Transport: &LoggingInterceptor{
			// http.DefaultTransportを利用しリクエストを送信
			Transport: http.DefaultTransport,
		},
	}
	cli.Get("https://example.com")
}
