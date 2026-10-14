package news

import (
	"context"
	"testing"
)

func unit(dim, hot int) []float32 {
	v := make([]float32, dim)
	v[hot] = 1
	return v
}

func TestVecLiteral(t *testing.T) {
	if got := vecLiteral([]float32{1, 0.5, -2}); got != "[1,0.5,-2]" {
		t.Fatalf("got %s", got)
	}
}

func TestHalfvecRoundTripAndCosine(t *testing.T) {
	ctx := context.Background()
	var ids [3]int64
	vecs := [][]float32{unit(768, 0), unit(768, 1), unit(768, 0)}
	for i, v := range vecs {
		err := db.QueryRow(ctx,
			`INSERT INTO clusters (centroid) VALUES ($1::halfvec) RETURNING id`,
			vecLiteral(v)).Scan(&ids[i])
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { db.Exec(ctx, `DELETE FROM clusters WHERE id = ANY($1)`, ids[:]) })

	// cosine nearest to unit(0): ids[0] and ids[2] at distance 0, ids[1] at 1.
	rows, err := db.Query(ctx,
		`SELECT id, centroid <=> $1::halfvec AS dist FROM clusters
		 WHERE id = ANY($2) ORDER BY dist, id`, vecLiteral(unit(768, 0)), ids[:])
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []int64
	var dists []float64
	for rows.Next() {
		var id int64
		var d float64
		if err := rows.Scan(&id, &d); err != nil {
			t.Fatal(err)
		}
		got = append(got, id)
		dists = append(dists, d)
	}
	if len(got) != 3 || got[2] != ids[1] || dists[0] > 1e-3 || dists[2] < 0.99 {
		t.Fatalf("unexpected order/dist: %v %v", got, dists)
	}
}

func TestPgvectorVersion(t *testing.T) {
	var v string
	err := db.QueryRow(context.Background(),
		`SELECT extversion FROM pg_extension WHERE extname='vector'`).Scan(&v)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("pgvector %s", v)
}
