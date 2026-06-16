package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/MonarchRyuzaki/db-internals/internal/storage"
)

func main() {
	fmt.Println("Initializing Storage Engine...")

	// Create or open the DB in the current directory
	db, err := storage.NewBTree("repl_store", ".")
	if err != nil {
		fmt.Printf("Failed to initialize database: %v\n", err)
		os.Exit(1)
	}

	// Start the background vacuum process to clean up tombstones every 10 seconds
	db.StartVacuumRoutine(10 * time.Second)
	
	// Start the background checkpoint process to limit recovery time (every 30 seconds for testing)
	db.StartCheckpointRoutine(30 * time.Second)

	// Ensure we flush everything when we exit!
	defer db.Close()

	fmt.Println("Database initialized successfully! (Running on custom B-Tree with Page-Level Latching)")
	fmt.Println("Available commands:")
	fmt.Println("  SET <key> <value>")
	fmt.Println("  GET <key>")
	fmt.Println("  DELETE <key>")
	fmt.Println("  EXIT")
	fmt.Println("-------------------------------------------------")

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("db> ")
		if !scanner.Scan() {
			break
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		// Split into at most 3 parts: COMMAND, KEY, VALUE
		parts := strings.SplitN(line, " ", 3)
		command := strings.ToUpper(parts[0])

		switch command {
		case "SET":
			if len(parts) < 3 {
				fmt.Println("Usage: SET <key> <value>")
				continue
			}
			key := []byte(parts[1])
			value := []byte(parts[2])

			start := time.Now()
			err := db.Insert(key, value)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
			} else {
				fmt.Printf("OK (took %v)\n", time.Since(start))
			}

		case "GET":
			if len(parts) < 2 {
				fmt.Println("Usage: GET <key>")
				continue
			}
			key := []byte(parts[1])

			start := time.Now()
			val, err := db.Find(key)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
			} else {
				fmt.Printf("\"%s\" (took %v)\n", string(val), time.Since(start))
			}

		case "DELETE":
			if len(parts) < 2 {
				fmt.Println("Usage: DELETE <key>")
				continue
			}
			key := []byte(parts[1])

			start := time.Now()
			err := db.Delete(key)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
			} else {
				fmt.Printf("OK (took %v)\n", time.Since(start))
			}

		case "EXIT":
			fmt.Println("Shutting down database...")
			return

		default:
			fmt.Printf("Unknown command: %s\n", command)
		}
	}
}
