package main

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"strings"
)

var _ http.RoundTripper = &Interceptor{}

type Interceptor struct {
	Transport http.RoundTripper
}

func (i *Interceptor) RoundTrip(req *http.Request) (*http.Response, error) {
	// dump request
	b, err := httputil.DumpRequest(req, true)
	if err != nil {
		fmt.Println(err)
	} else {
		fmt.Println("Request:")
		dumped := strings.Split(string(b), "\n")
		for _, line := range dumped {
			fmt.Printf(">> %s\n", line)
		}
	}

	resp, err := i.Transport.RoundTrip(req)

	// dump response
	b, err = httputil.DumpResponse(resp, true)
	if err != nil {
		fmt.Println(err)
	} else {
		fmt.Println("Response:")
		dumped := strings.Split(string(b), "\n")
		for _, line := range dumped {
			fmt.Printf("<< %s\n", line)
		}
	}

	return resp, err
}

func main() {
	client := &http.Client{
		Transport: &Interceptor{
			Transport: http.DefaultTransport,
		},
	}

	body := strings.NewReader("Hello, World!")
	req, err := http.NewRequest("GET", "http://localhost:8080", body)
	if err != nil {
		fmt.Println(err)
		return
	}
	_, err = client.Do(req)
	if err != nil {
		fmt.Println(err)
		return
	}
}
