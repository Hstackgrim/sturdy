package main

import (
	"flag"
	"log"
	"strings"
	"time"

	"github.com/keighl/postmark"

	"getsturdy.com/api/pkg/newsletter"
)

func main() {
	serverToken := flag.String("server-token", "", "Postmark server token")
	flag.Parse()
	if serverToken == nil || *serverToken == "" {
		log.Fatal("server-token is required")
	}

	pm := postmark.NewClient(*serverToken, "")

	receivers := []string{
		"gustav@westling.dev",
		// "gustav@getsturdy.com",
	}

	subject := "This week at Sturdy #16 – What's new in Sturdy v1.7.0"

	for _, receiver := range receivers {
		receiver = strings.TrimSpace(receiver)
		log.Println("Sending to", receiver)
		newsletter.Send(pm, subject, "output/2022-04-13.html", receiver)
		time.Sleep(time.Second)
	}
}
