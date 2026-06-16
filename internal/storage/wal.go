package storage

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"
	"sync"
)

// Log Operation Types
const (
	LogOpInsert   uint8 = 1
	LogOpDelete   uint8 = 2
	LogOpFullPage uint8 = 3 // Full Page Write (Backup for torn pages)
	LogOpCommit   uint8 = 4
	LogOpAbort    uint8 = 5
	LogOpCLR      uint8 = 6 // Compensation Log Record (Repeating History during Undo)
	LogOpCheckpoint uint8 = 7 // Fuzzy Checkpoint
)

// LogRecord represents a single physiological operation in the WAL.
type LogRecord struct {
	LSN    uint64 // Log Sequence Number (monotonically increasing)
	TxnID  uint32 // Transaction ID
	PageID uint32 // Physical Page ID
	OpType uint8  // Logical Operation
	Key    []byte
	Value  []byte // Holds value, OR holds full 4KB backup if OpType is LogOpFullPage
	
	// TODO(Phase 2 - Transactions): Add PrevLSN uint64 
	// To perform the Undo phase, each log record must store the LSN of the 
	// *previous* log record written by the same transaction to allow backward scanning.
}

// Serialize converts a LogRecord into a binary byte slice.
// Layout: [Size(4)][LSN(8)][TxnID(4)][PageID(4)][Op(1)][KeyLen(2)][ValLen(4)][Key...][Val...][CRC(4)]
func (l *LogRecord) Serialize() []byte {
	totalSize := 4 + 8 + 4 + 4 + 1 + 2 + 4 + len(l.Key) + len(l.Value) + 4
	buf := make([]byte, totalSize)

	binary.LittleEndian.PutUint32(buf[0:4], uint32(totalSize))
	binary.LittleEndian.PutUint64(buf[4:12], l.LSN)
	binary.LittleEndian.PutUint32(buf[12:16], l.TxnID)
	binary.LittleEndian.PutUint32(buf[16:20], l.PageID)
	buf[20] = l.OpType
	binary.LittleEndian.PutUint16(buf[21:23], uint16(len(l.Key)))
	binary.LittleEndian.PutUint32(buf[23:27], uint32(len(l.Value)))

	offset := 27
	copy(buf[offset:], l.Key)
	offset += len(l.Key)
	copy(buf[offset:], l.Value)
	offset += len(l.Value)

	// Calculate CRC32 checksum over the payload (excluding Size and CRC fields) to protect against torn logs
	checksum := crc32.ChecksumIEEE(buf[4:offset])
	binary.LittleEndian.PutUint32(buf[offset:offset+4], checksum)

	return buf
}

// DeserializeLogRecord parses a byte slice back into a LogRecord.
// It verifies the CRC32 checksum to protect against torn log entries.
func DeserializeLogRecord(data []byte) (LogRecord, error) {
	if len(data) < 31 {
		return LogRecord{}, errors.New("log record too small")
	}

	size := binary.LittleEndian.Uint32(data[0:4])
	if int(size) != len(data) {
		return LogRecord{}, errors.New("log record size mismatch")
	}

	crcOffset := len(data) - 4
	expectedCRC := binary.LittleEndian.Uint32(data[crcOffset:])
	actualCRC := crc32.ChecksumIEEE(data[4:crcOffset])

	if expectedCRC != actualCRC {
		return LogRecord{}, errors.New("log record checksum mismatch (torn log detected)")
	}

	rec := LogRecord{}
	rec.LSN = binary.LittleEndian.Uint64(data[4:12])
	rec.TxnID = binary.LittleEndian.Uint32(data[12:16])
	rec.PageID = binary.LittleEndian.Uint32(data[16:20])
	rec.OpType = data[20]

	keyLen := binary.LittleEndian.Uint16(data[21:23])
	valLen := binary.LittleEndian.Uint32(data[23:27])

	offset := 27
	// Zero-copy deserialization: slice directly from the buffer!
	rec.Key = data[offset : offset+int(keyLen)]
	offset += int(keyLen)
	rec.Value = data[offset : offset+int(valLen)]

	return rec, nil
}

// WAL represents the Write-Ahead Log append-only file.
type WAL struct {
	file       *os.File
	mu         sync.Mutex
	currentLSN uint64
}

// NewWAL opens or creates the WAL file.
func NewWAL(path string) (*WAL, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0666)
	if err != nil {
		return nil, err
	}

	stat, _ := file.Stat()

	wal := &WAL{
		file:       file,
		currentLSN: uint64(stat.Size()), // LSN is exactly the physical byte offset!
	}

	return wal, nil
}

// Append writes a new record to the WAL and fsyncs to ensure durability.
func (w *WAL) Append(txnID, pageID uint32, opType uint8, key, value []byte) (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	lsn := w.currentLSN

	rec := &LogRecord{
		LSN:    lsn,
		TxnID:  txnID,
		PageID: pageID,
		OpType: opType,
		Key:    key,
		Value:  value,
	}

	data := rec.Serialize()

	// 1. Write to OS Buffer
	_, err := w.file.Write(data)
	if err != nil {
		return 0, err
	}

	// Advance LSN by the exact bytes written
	w.currentLSN += uint64(len(data))

	// 2. Fsync to physical disk (Durability)
	err = w.file.Sync()
	if err != nil {
		return 0, err
	}

	return lsn, nil
}

// ReadRecord reads a single log record from a physical byte offset (LSN).
func (w *WAL) ReadRecord(lsn uint64) (LogRecord, uint32, error) {
	sizeBuf := make([]byte, 4)
	_, err := w.file.ReadAt(sizeBuf, int64(lsn))
	if err != nil {
		return LogRecord{}, 0, err
	}

	size := binary.LittleEndian.Uint32(sizeBuf)
	if size < 31 {
		return LogRecord{}, 0, errors.New("corrupted log record size")
	}

	buf := make([]byte, size)
	_, err = w.file.ReadAt(buf, int64(lsn))
	if err != nil {
		return LogRecord{}, 0, err
	}

	rec, err := DeserializeLogRecord(buf)
	return rec, size, err
}

// Close gracefully shuts down the WAL.
func (w *WAL) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Close()
}

// WriteCheckpoint writes a Checkpoint record and flushes it, returning the LSN.
// It serializes the Dirty Page Table into the Value payload.
func (w *WAL) WriteCheckpoint(dpt map[uint32]uint64) (uint64, error) {
	// Serialize DPT: 4 bytes len + 12 bytes per entry
	buf := make([]byte, 4 + len(dpt)*12)
	binary.LittleEndian.PutUint32(buf[0:4], uint32(len(dpt)))
	offset := 4
	for pageID, recLSN := range dpt {
		binary.LittleEndian.PutUint32(buf[offset:offset+4], pageID)
		binary.LittleEndian.PutUint64(buf[offset+4:offset+12], recLSN)
		offset += 12
	}
	
	// Append the checkpoint record (TxnID=0, PageID=0)
	lsn, err := w.Append(0, 0, LogOpCheckpoint, nil, buf)
	if err != nil {
		return 0, err
	}
	return lsn, nil
}
