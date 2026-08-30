// Command seeds-export dumps the idea bank as a JSON array in the same wire
// format the seeds were stored in, ready to POST to the Feed Runner backend.
//
//	go run ./cmd/seeds-export ./data/ideas.db ./data/seeds.json
//	go run ./cmd/seeds-export ./data/ideas.db            # to stdout
package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"

	_ "modernc.org/sqlite"

	"github.com/Yash-007/feed-engine/internal/model"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: seeds-export <ideas.db> [out.json]")
		os.Exit(2)
	}

	db, err := sql.Open("sqlite", os.Args[1])
	if err != nil {
		panic(err)
	}
	defer db.Close()

	rows, err := db.Query(`SELECT id, captured_at_millis, source, platform, category,
	  theme_tags, tension, angle_hint, shelf_life, post_author, post_text,
	  source_post_url, source_post_id, visual, status
	  FROM idea_seeds ORDER BY created_at DESC, id`)
	if err != nil {
		panic(err)
	}
	defer rows.Close()

	seeds := []model.IdeaSeed{}
	for rows.Next() {
		var s model.IdeaSeed
		var tags sql.NullString
		var visual int
		if err := rows.Scan(&s.ID, &s.CapturedAtMillis, &s.Source, &s.Platform,
			&s.Category, &tags, &s.Tension, &s.AngleHint, &s.ShelfLife,
			&s.PostAuthor, &s.PostText, &s.SourcePostURL, &s.SourcePostID,
			&visual, &s.Status); err != nil {
			panic(err)
		}
		s.Visual = visual == 1
		if tags.Valid && tags.String != "" {
			_ = json.Unmarshal([]byte(tags.String), &s.ThemeTags)
		}
		seeds = append(seeds, s)
	}
	if err := rows.Err(); err != nil {
		panic(err)
	}

	out, err := json.MarshalIndent(seeds, "", "  ")
	if err != nil {
		panic(err)
	}
	if len(os.Args) > 2 {
		if err := os.WriteFile(os.Args[2], out, 0o644); err != nil {
			panic(err)
		}
		fmt.Fprintf(os.Stderr, "wrote %d seeds to %s\n", len(seeds), os.Args[2])
		return
	}
	fmt.Println(string(out))
}
