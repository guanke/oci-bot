package main

import (
	"context"
	"fmt"
	"time"

	"oci-bot/ippure"
)

func main() {
	ip := "217.142.240.193"
	fmt.Println("Testing IP purity check for", ip)
	fmt.Println("This may take 15-20 seconds...")

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	info, err := ippure.Check(ctx, ip)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Println("\n" + info.FormatResult())
}
