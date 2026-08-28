package config

import (
	"bufio"
	"os"
	"strings"
)

// envFileLoad parses a simple .env file (KEY=value per line) and exports those
// variables into the process environment. Blank lines and '#' comments are
// ignored. Quoted values have their surrounding quotes stripped. This mirrors
// dotenv behavior closely enough for our purposes.
func envFileLoad(path string) {
	f, err := os.Open(path)
	if err != nil {
		return // missing/undecipherable .env is not fatal (Python uses load_dotenv)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		kv := strings.SplitN(line, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		val := strings.TrimSpace(kv[1])
		val = unquote(val)
		os.Setenv(key, val)
	}
}

func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
