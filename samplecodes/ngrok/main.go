package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"golang.ngrok.com/ngrok"
	"golang.ngrok.com/ngrok/config"
)

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func ngrokListener(ctx context.Context) (ngrok.Tunnel, error) {
	return ngrok.Listen(ctx,
		config.HTTPEndpoint(
			config.WithOAuth("google",
				config.WithAllowOAuthEmail(os.Getenv("NGROK_ALLOW_EMAIL")),
				// config.WithAllowOAuthDomain("acme.org"),
			),
		),
		ngrok.WithAuthtoken(os.Getenv("NGROK_AUTHTOKEN")),
	)
}

func run(ctx context.Context) error {
	listener, err := ngrokListener(ctx)
	if err != nil {
		return err
	}

	log.Println("Ingress established at:", listener.URL())
	log.Println("Ingress established at:", "https://"+listener.Addr().String())

	return http.Serve(listener, http.HandlerFunc(handler))
}

func handler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "Hello from ngrok-go!")
}
