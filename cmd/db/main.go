package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
)

func main() {
	conn, err := net.Dial("tcp", "localhost:8080")
	if err != nil {
		fmt.Printf("Failed to connect to server: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	// Read welcome message
	serverScanner := bufio.NewScanner(conn)
	if serverScanner.Scan() {
		fmt.Println(serverScanner.Text())
	}

	// Goroutine to print server responses
	go func() {
		for serverScanner.Scan() {
			fmt.Println(serverScanner.Text())
			fmt.Print("db> ")
		}
		if err := serverScanner.Err(); err != nil {
			fmt.Printf("\nError reading from server: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}()

	fmt.Print("db> ")
	clientScanner := bufio.NewScanner(os.Stdin)
	for clientScanner.Scan() {
		line := strings.TrimSpace(clientScanner.Text())
		if line == "" {
			fmt.Print("db> ")
			continue
		}

		if strings.ToUpper(line) == "EXIT" {
			fmt.Fprintln(conn, "EXIT")
			return
		}

		fmt.Fprintln(conn, line)
	}
}
