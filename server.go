package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"github.com/MonarchRyuzaki/db-internals/internal/engine"
	"github.com/MonarchRyuzaki/db-internals/internal/storage"
)

// ClientState represents the state of a connected client.
type ClientState struct {
	Conn net.Conn
	InTx bool
	TxID storage.TxnID
}

// NewClientState initializes a new ClientState.
func NewClientState(conn net.Conn) *ClientState {
	return &ClientState{
		Conn: conn,
		InTx: false,
	}
}

// handleConnection handles incoming client connections.
func handleConnection(conn net.Conn, mvccDB *engine.DB, txMgr *storage.TransactionManager) {
	defer conn.Close()
	client := NewClientState(conn)

	scanner := bufio.NewScanner(conn)

	fmt.Fprintln(conn, "Welcome to the DB server.")

	for scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}

		parts := strings.Fields(text)
		cmd := strings.ToUpper(parts[0])

		switch cmd {
		case "BEGIN":
			if client.InTx {
				fmt.Fprintln(conn, "ERR BEGIN calls can not be nested")
			} else {
				client.InTx = true
				client.TxID = txMgr.Begin()
				fmt.Fprintln(conn, "OK")
			}
		case "COMMIT":
			if !client.InTx {
				fmt.Fprintln(conn, "ERR COMMIT without BEGIN")
			} else {
				txMgr.Commit(client.TxID)
				client.InTx = false
				fmt.Fprintln(conn, "OK")
			}
		case "ROLLBACK":
			if !client.InTx {
				fmt.Fprintln(conn, "ERR ROLLBACK without BEGIN")
			} else {
				txMgr.Rollback(client.TxID)
				client.InTx = false
				fmt.Fprintln(conn, "OK")
			}
		case "QUIT", "EXIT":
			fmt.Fprintln(conn, "Goodbye!")
			return
		default:
			if client.InTx {
				executeCommand(conn, mvccDB, client.TxID, text)
			} else {
				// Auto-commit mode
				txID := txMgr.Begin()
				executeCommand(conn, mvccDB, txID, text)
				txMgr.Commit(txID)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("Error reading from connection: %v\n", err)
	}
}

func executeCommand(conn net.Conn, mvccDB *engine.DB, txID storage.TxnID, line string) {
	parts := strings.SplitN(line, " ", 3)
	command := strings.ToUpper(parts[0])

	switch command {
	case "SET":
		if len(parts) < 3 {
			fmt.Fprintln(conn, "Usage: SET <key> <value>")
			return
		}
		key := parts[1]
		value := parts[2]

		start := time.Now()
		err := mvccDB.Set(txID, key, value)
		if err != nil {
			fmt.Fprintf(conn, "Error: %v\n", err)
		} else {
			fmt.Fprintf(conn, "OK (took %v)\n", time.Since(start))
		}

	case "GET":
		if len(parts) < 2 {
			fmt.Fprintln(conn, "Usage: GET <key>")
			return
		}
		key := parts[1]

		start := time.Now()
		val, err := mvccDB.Get(txID, key)
		if err != nil {
			fmt.Fprintf(conn, "Error: %v\n", err)
		} else {
			fmt.Fprintf(conn, "\"%s\" (took %v)\n", val, time.Since(start))
		}

	case "DELETE":
		if len(parts) < 2 {
			fmt.Fprintln(conn, "Usage: DELETE <key>")
			return
		}
		key := parts[1]

		start := time.Now()
		err := mvccDB.Delete(txID, key)
		if err != nil {
			fmt.Fprintf(conn, "Error: %v\n", err)
		} else {
			fmt.Fprintf(conn, "OK (took %v)\n", time.Since(start))
		}

	default:
		fmt.Fprintf(conn, "Unknown command: %s\n", command)
	}
}

func main() {
	// Initialize Storage Engine
	db, err := storage.NewBTree("repl_store", ".")
	if err != nil {
		log.Fatalf("Failed to initialize database: %v\n", err)
	}
	defer db.Close()

	// Start the background checkpoint process
	db.StartCheckpointRoutine(30 * time.Second)

	db.StartVacuumRoutine(10 * time.Second)
	
	// Wrap the BTree in our MVCC Engine
	mvccDB := engine.NewDB(db)
	txMgr := storage.NewTransactionManager()

	port := "8080"
	listener, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatalf("Failed to listen on port %s: %v", port, err)
	}
	defer listener.Close()

	log.Printf("Server listening on TCP port %s...\n", port)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Failed to accept connection: %v\n", err)
			continue
		}

		log.Printf("Accepted connection from %s\n", conn.RemoteAddr().String())
		go handleConnection(conn, mvccDB, txMgr)
	}
}
