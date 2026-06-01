package storage

import (
	"encoding/binary"
)

type KeyCell struct {
	ChildPageID uint32
	Key         []byte
}

type KVCell struct {
	Flag  uint8
	Key   []byte
	Value []byte
}

// KeyCell Layout: [ChildPageID (4b)][KeyLen (2b)][Key...]
func (k *KeyCell) Size() int {
	return 4 + 2 + len(k.Key)
}

func (k *KeyCell) Serialize() []byte {
	size := k.Size()
	buf := make([]byte, size)
	binary.LittleEndian.PutUint32(buf[0:4], k.ChildPageID)
	binary.LittleEndian.PutUint16(buf[4:6], uint16(len(k.Key)))
	copy(buf[6:], k.Key)
	return buf
}

func DeserializeKeyCell(data []byte) *KeyCell {
	k := &KeyCell{}
	k.ChildPageID = binary.LittleEndian.Uint32(data[0:4])
	keyLen := binary.LittleEndian.Uint16(data[4:6])
	k.Key = make([]byte, keyLen)
	copy(k.Key, data[6:6+keyLen])
	return k
}

// KVCell Layout: [Flag (1b)][KeyLen (2b)][ValueLen (2b)][Key...][Value...]
func (k *KVCell) Size() int {
	return 1 + 2 + 2 + len(k.Key) + len(k.Value)
}

func (k *KVCell) Serialize() []byte {
	size := k.Size()
	buf := make([]byte, size)
	buf[0] = k.Flag
	binary.LittleEndian.PutUint16(buf[1:3], uint16(len(k.Key)))
	binary.LittleEndian.PutUint16(buf[3:5], uint16(len(k.Value)))
	copy(buf[5:5+len(k.Key)], k.Key)
	copy(buf[5+len(k.Key):], k.Value)
	return buf
}

func DeserializeKVCell(data []byte) *KVCell {
	k := &KVCell{}
	k.Flag = data[0]
	keyLen := binary.LittleEndian.Uint16(data[1:3])
	valLen := binary.LittleEndian.Uint16(data[3:5])
	k.Key = make([]byte, keyLen)
	k.Value = make([]byte, valLen)
	copy(k.Key, data[5:5+keyLen])
	copy(k.Value, data[5+keyLen:5+keyLen+valLen])
	return k
}

func NewKeyCell(pageID uint32, key []byte) *KeyCell {
	return &KeyCell{
		ChildPageID: pageID,
		Key:         key,
	}
}

func NewKVCell(flag uint8, key []byte, value []byte) *KVCell {
	return &KVCell{
		Flag:  flag,
		Key:   key,
		Value: value,
	}
}
