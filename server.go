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
				mvccDB.Commit(client.TxID)
				client.InTx = false
				fmt.Fprintln(conn, "OK")
			}
		case "ROLLBACK":
			if !client.InTx {
				fmt.Fprintln(conn, "ERR ROLLBACK without BEGIN")
			} else {
				mvccDB.Rollback(client.TxID)
				client.InTx = false
				fmt.Fprintln(conn, "OK")
			}
		case "QUIT", "EXIT":
			fmt.Fprintln(conn, "Goodbye!")
			return
		default:
			if client.InTx {
				err := executeCommand(conn, mvccDB, client.TxID, text)
				if err != nil && strings.Contains(err.Error(), "write-write conflict") {
					client.InTx = false
					fmt.Fprintln(conn, "Transaction automatically aborted due to conflict.")
				}
			} else {
				// Auto-commit mode
				txID := txMgr.Begin()
				err := executeCommand(conn, mvccDB, txID, text)
				if err == nil {
					mvccDB.Commit(txID)
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("Error reading from connection: %v\n", err)
	}
}

func executeCommand(conn net.Conn, mvccDB *engine.DB, txID storage.TxnID, line string) error {
	parts := strings.SplitN(line, " ", 3)
	command := strings.ToUpper(parts[0])

	switch command {
	case "SET":
		if len(parts) < 3 {
			fmt.Fprintln(conn, "Usage: SET <key> <value>")
			return nil
		}
		key := parts[1]
		value := parts[2]

		start := time.Now()
		err := mvccDB.Set(txID, key, value)
		if err != nil {
			fmt.Fprintf(conn, "Error: %v\n", err)
			return err
		} else {
			fmt.Fprintf(conn, "OK (took %v)\n", time.Since(start))
		}

	case "GET":
		if len(parts) < 2 {
			fmt.Fprintln(conn, "Usage: GET <key>")
			return nil
		}
		key := parts[1]

		start := time.Now()
		val, err := mvccDB.Get(txID, key)
		if err != nil {
			fmt.Fprintf(conn, "Error: %v\n", err)
			return err
		} else {
			fmt.Fprintf(conn, "\"%s\" (took %v)\n", val, time.Since(start))
		}

	case "DELETE":
		if len(parts) < 2 {
			fmt.Fprintln(conn, "Usage: DELETE <key>")
			return nil
		}
		key := parts[1]

		start := time.Now()
		err := mvccDB.Delete(txID, key)
		if err != nil {
			fmt.Fprintf(conn, "Error: %v\n", err)
			return err
		} else {
			fmt.Fprintf(conn, "OK (took %v)\n", time.Since(start))
		}

	default:
		fmt.Fprintf(conn, "Unknown command: %s\n", command)
	}
	return nil
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
	txMgr := storage.NewTransactionManager()
	mvccDB := engine.NewDB(db, txMgr)

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
