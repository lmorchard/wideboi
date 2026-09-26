package commands

import (
	"errors"
	"strings"
	"unicode"
)

var (
	ErrUnclosedQuote = errors.New("unclosed quote in command line")
)

// ParseLine parses a command line string into a command name and argument slice.
// A leading ':' is stripped if present. Arguments enclosed in single or double
// quotes are preserved as single tokens.
func ParseLine(line string) (name string, args []string, err error) {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, ":") {
		line = strings.TrimPrefix(line, ":")
		line = strings.TrimSpace(line)
	}
	if line == "" {
		return "", nil, nil
	}

	tokens, err := tokenize(line)
	if err != nil {
		return "", nil, err
	}
	if len(tokens) == 0 {
		return "", nil, nil
	}
	return tokens[0], tokens[1:], nil
}

func tokenize(s string) ([]string, error) {
	var tokens []string
	var cur strings.Builder
	var inQuote rune
	var escaped bool
	var tokenStarted bool

	for _, r := range s {
		if escaped {
			cur.WriteRune(r)
			tokenStarted = true
			escaped = false
			continue
		}

		if r == '\\' {
			escaped = true
			tokenStarted = true
			continue
		}

		if inQuote != 0 {
			if r == inQuote {
				inQuote = 0
			} else {
				cur.WriteRune(r)
			}
			continue
		}

		if r == '"' || r == '\'' {
			inQuote = r
			tokenStarted = true
			continue
		}

		if unicode.IsSpace(r) {
			if tokenStarted {
				tokens = append(tokens, cur.String())
				cur.Reset()
				tokenStarted = false
			}
			continue
		}

		cur.WriteRune(r)
		tokenStarted = true
	}

	if inQuote != 0 {
		return nil, ErrUnclosedQuote
	}
	if escaped {
		cur.WriteRune('\\')
	}
	if tokenStarted {
		tokens = append(tokens, cur.String())
	}

	return tokens, nil
}
