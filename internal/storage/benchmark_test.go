package storage

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"testing"
)

type Product struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Category    string  `json:"category"`
	Price       float64 `json:"price"`
	Quantity    int     `json:"quantity"`
}

func BenchmarkBTree_InsertSequential(b *testing.B) {
	dir := b.TempDir()
	tree, err := NewBTree("bench_seq", dir)
	if err != nil {
		b.Fatalf("Failed to create BTree: %v", err)
	}

	txMgr := NewTransactionManager()
	value := []byte("bench_value_data")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("key_%08d", i))
		err := tree.Insert(key, value, txMgr)
		if err != nil {
			b.Fatalf("Insert failed: %v", err)
		}
	}
}

func BenchmarkBTree_InsertRandom(b *testing.B) {
	dir := b.TempDir()
	tree, err := NewBTree("bench_rand", dir)
	if err != nil {
		b.Fatalf("Failed to create BTree: %v", err)
	}

	txMgr := NewTransactionManager()
	value := []byte("bench_value_data")

	// Pre-generate keys to not measure rand.Intn overhead during benchmark
	keys := make([][]byte, b.N)
	for i := 0; i < b.N; i++ {
		keys[i] = []byte(fmt.Sprintf("key_%08d", rand.Intn(b.N)))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := tree.Insert(keys[i], value, txMgr)
		if err != nil {
			b.Fatalf("Insert failed: %v", err)
		}
	}
}

func BenchmarkBTree_Find(b *testing.B) {
	dir := b.TempDir()
	tree, err := NewBTree("bench_find", dir)
	if err != nil {
		b.Fatalf("Failed to create BTree: %v", err)
	}

	txMgr := NewTransactionManager()
	value := []byte("bench_value_data")
	numKeys := 10000

	keys := make([][]byte, numKeys)
	for i := 0; i < numKeys; i++ {
		keys[i] = []byte(fmt.Sprintf("key_%08d", i))
		tree.Insert(keys[i], value, txMgr)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := keys[i%numKeys]
		_, err := tree.Find(key)
		if err != nil {
			b.Fatalf("Find failed: %v", err)
		}
	}
}

func BenchmarkBTree_LargePayload(b *testing.B) {
	dir := b.TempDir()
	tree, err := NewBTree("bench_large", dir)
	if err != nil {
		b.Fatalf("Failed to create BTree: %v", err)
	}

	txMgr := NewTransactionManager()
	// 10KB payload testing overflow logic
	largePayload := make([]byte, 10000)
	for i := 0; i < len(largePayload); i++ {
		largePayload[i] = byte(i % 256)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("key_%08d", i))
		err := tree.Insert(key, largePayload, txMgr)
		if err != nil {
			b.Fatalf("Insert failed: %v", err)
		}
	}
}

func BenchmarkBTree_ParallelReadWrite(b *testing.B) {
	dir := b.TempDir()
	tree, err := NewBTree("bench_parallel", dir)
	if err != nil {
		b.Fatalf("Failed to create BTree: %v", err)
	}

	txMgr := NewTransactionManager()
	value := []byte("bench_value_data")
	// Pre-load some data
	for i := 0; i < 1000; i++ {
		key := []byte(fmt.Sprintf("key_%08d", i))
		tree.Insert(key, value, txMgr)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			i++
			key := []byte(fmt.Sprintf("key_%08d", i%10000))
			if i%2 == 0 {
				// 50% Writes
				tree.Insert(key, value, txMgr)
			} else {
				// 50% Reads
				tree.Find(key) // Ignore error since key might not exist yet
			}
		}
	})
}

func BenchmarkBTree_EcommerceWorkload(b *testing.B) {
	dir := b.TempDir()
	tree, err := NewBTree("ecommerce_bench", dir)
	if err != nil {
		b.Fatalf("Failed to create BTree: %v", err)
	}

	txMgr := NewTransactionManager()
	// Simulating an 80/20 Read/Write workload
	// 80% of operations are users browsing products (Reads)
	// 20% of operations are merchants updating prices/stock or adding products (Writes)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			i++
			prodID := fmt.Sprintf("prod_%08d", i%100000)
			
			if i%10 < 2 {
				// 20% Writes
				prod := Product{
					ID:          prodID,
					Name:        fmt.Sprintf("Wireless Headphones Model %d", i),
					Description: "High quality noise cancelling wireless headphones with 40-hour battery life.",
					Category:    "Electronics",
					Price:       rand.Float64() * 300.0,
					Quantity:    rand.Intn(100),
				}
				data, _ := json.Marshal(prod)
				tree.Insert([]byte(prodID), data, txMgr)
			} else {
				// 80% Reads
				_, _ = tree.Find([]byte(prodID)) 
			}
		}
	})
}
