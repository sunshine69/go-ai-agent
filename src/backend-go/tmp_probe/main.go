package main

import (
	"database/sql"
	"encoding/binary"
	"fmt"
	"log"
	"math"

	vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	_ "github.com/mattn/go-sqlite3"
)

func blob(vals []float32) []byte {
	out := make([]byte, len(vals)*4)
	for i, v := range vals {
		binary.LittleEndian.PutUint32(out[i*4:], math.Float32bits(v))
	}
	return out
}

// 384-dim vectors
func vecf(fill func(i int) float32) []float32 {
	v := make([]float32, 384)
	for i := range v {
		v[i] = fill(i)
	}
	return v
}

func main() {
	vec.Auto()
	database, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()

	// Single-table vec0 for embeddings, all columns.
	const schema = `CREATE VIRTUAL TABLE IF NOT EXISTS embeddings USING vec0 (
	id          INTEGER PRIMARY KEY,
	doc_id      TEXT NOT NULL,
	title       TEXT NOT NULL,
	category    TEXT NOT NULL,
	url         TEXT NOT NULL,
	page_number INTEGER
);
CREATE VIRTUAL TABLE IF NOT EXISTS embeddings_vec USING vec0 (
	embedding float[384]
);`
	// Single-table with inline vector column (user's pattern).
	const inline = `CREATE VIRTUAL TABLE IF NOT EXISTS embeddings USING vec0 (
	id          INTEGER PRIMARY KEY,
	doc_id      TEXT NOT NULL,
	title       TEXT NOT NULL,
	category    TEXT NOT NULL,
	url         TEXT NOT NULL,
	page_number INTEGER,
	embedding   float[384]
);`
	if _, err := database.Exec(inline); err != nil {
		log.Fatalf("inline create: %v", err)
	}
	fmt.Println("inline create OK")

	// db_test.go TestCosineSimilarityOrdering-style: 3 vectors, brute-force plain scan, vec_distance_cosine scalar.
	v0 := vecf(func(i int) float32 { return float32(i) })        // +x-ish
	v1 := vecf(func(i int) float32 { return float32(i) })        // placeholder
	v1 = vecf(func(i int) float32 { return float32(1 + i/100) }) // +x & +y-ish
	v2 := vecf(func(i int) float32 { return 0 })
	if _, err := database.Exec(`INSERT INTO embeddings(doc_id,title,category,url,page_number,embedding) VALUES (?,?,?,?,?,?)`, "v0", "v0", "c", "u", 0, blob(v0)); err != nil {
		log.Fatalf("insert v0: %v", err)
	}
	// v1 closer to query (1,1,0,0...) : give it a y component
	v1 = vecf(func(i int) float32 { return float32(1)*0.7 + float32(i%2)*0.5 })
	if _, err := database.Exec(`INSERT INTO embeddings(doc_id,title,category,url,page_number,embedding) VALUES (?,?,?,?,?,?)`, "v1", "v1", "c", "u", 0, blob(v1)); err != nil {
		log.Fatalf("insert v1: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO embeddings(doc_id,title,category,url,page_number,embedding) VALUES (?,?,?,?,?,?)`, "v2", "v2", "c", "u", 0, blob(v2)); err != nil {
		log.Fatalf("insert v2: %v", err)
	}

	queryVec := vecf(func(i int) float32 {
		if i < 100 {
			return 1
		}
		return 1
	})
	rows, err := database.Query(`
		SELECT doc_id, vec_distance_cosine(embedding, ?) AS dist
		FROM embeddings
		ORDER BY dist ASC
		LIMIT 3`,
		blob(queryVec))
	if err != nil {
		log.Fatalf("brute-force scalar scan on vec0: %v", err)
	}
	defer rows.Close()
	var results []struct {
		doc  string
		dist float64
	}
	for rows.Next() {
		var r struct {
			doc  string
			dist float64
		}
		if err := rows.Scan(&r.doc, &r.dist); err != nil {
			log.Fatalf("scan: %v", err)
		}
		results = append(results, r)
	}
	if len(results) != 3 {
		log.Fatalf("expected 3, got %d", len(results))
	}
	for _, r := range results {
		fmt.Printf("  %s dist=%.4f\n", r.doc, r.dist)
	}
	fmt.Println("brute-force scalar scan on vec0 -> OK")

	// Now KNN form on same table.
	rows2, err := database.Query(`
		SELECT doc_id, distance
		FROM embeddings
		WHERE embedding MATCH ?
			ORDER BY distance
			LIMIT ?`,
		blob(queryVec), 3)
	if err != nil {
		log.Fatalf("knn: %v", err)
	}
	defer rows2.Close()
	var n int
	for rows2.Next() {
		var doc string
		var dist float64
		if err := rows2.Scan(&doc, &dist); err != nil {
			log.Fatalf("knn scan: %v", err)
		}
		n++
		fmt.Printf("  knn %s dist=%.4f\n", doc, dist)
	}
	fmt.Printf("knn form -> OK (rows=%d)\n", n)
	fmt.Println("DONE")
}
