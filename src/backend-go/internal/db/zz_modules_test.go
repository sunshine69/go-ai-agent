package db

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/asg017/sqlite-vec-go-bindings/cgo"
	_ "github.com/mattn/go-sqlite3"
)

func TestProbeModules(t *testing.T) {
	vec.Auto()
	database, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	for _, mod := range []string{"vec0", "vec32_vec", "vec_each", "vec_distance_cosine", "vec_distance_l1", "vec_distance_l2"} {
		var result string
		if err := database.QueryRow(fmt.Sprintf(`SELECT 1 WHERE EXISTS (SELECT 1 FROM pragma_function_list WHERE name = '%s')`, mod)).Scan(&result); err != nil {
			t.Logf("probe func %s: scan err: %v", mod, err)
		} else {
			t.Logf("probe func %s: exists=%q", mod, result)
		}
	}

	// Try creating each candidate virtual table.
	for _, mod := range []string{"vec0", "vec32_vec", "vec_each"} {
		_, err := database.Exec(fmt.Sprintf(`CREATE VIRTUAL TABLE IF NOT EXISTS "vec32_vec" USING %s(embedding FLOAT32 NOT NULL);`, mod))
		t.Logf("CREATE VIRTUAL TABLE USING %s: err=%v", mod, err)
	}
}
