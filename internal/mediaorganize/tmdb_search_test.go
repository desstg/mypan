package mediaorganize

import (
	"testing"
)

func TestParseTMDBQueryID(t *testing.T) {
	cases := map[string]string{
		"980477":        "980477",
		"tmdb-980477":   "980477",
		"tmdb:980477":   "980477",
		"tmdbid=980477": "980477",
		"{tmdb-980477}": "980477",
		"TMDB-123":      "123",
		"tmdb-2012":     "2012",
		"哪吒之魔童闹海":       "",
		"暗战 1999":       "",
		"2012":          "",
		"2046":          "",
		"":              "",
	}
	for in, want := range cases {
		if got := parseTMDBQueryID(in); got != want {
			t.Fatalf("parseTMDBQueryID(%q)=%q want %q", in, got, want)
		}
	}
}

func TestNormalizeTMDBDetailIDAcceptsFourDigitSeriesID(t *testing.T) {
	for in, want := range map[string]string{
		"1396":     "1396",
		" 281495 ": "281495",
		"000123":   "123",
	} {
		got, err := normalizeTMDBDetailID(in)
		if err != nil || got != want {
			t.Fatalf("normalizeTMDBDetailID(%q)=%q, %v want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "0", "-1", "tmdb-123", "12345678901"} {
		if _, err := normalizeTMDBDetailID(in); err == nil {
			t.Fatalf("normalizeTMDBDetailID(%q) 应失败", in)
		}
	}
}
