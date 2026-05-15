package pr

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Ref identifies a pull request. Owner/Repo are empty when the user passed
// a bare number ("1234") — the caller fills them in from the current repo's
// remote.
type Ref struct {
	Owner  string
	Repo   string
	Number int
}

var (
	reNumber = regexp.MustCompile(`^\d+$`)
	reShort  = regexp.MustCompile(`^([\w.-]+)/([\w.-]+)#(\d+)$`)
)

// ParseRef accepts three forms:
//   - "1234"                                     → Ref{Number: 1234}
//   - "owner/repo#1234"                          → Ref{Owner:"owner", Repo:"repo", Number:1234}
//   - "https://github.com/owner/repo/pull/1234"  → same as above
func ParseRef(s string) (Ref, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Ref{}, fmt.Errorf("empty PR ref")
	}
	if reNumber.MatchString(s) {
		n, _ := strconv.Atoi(s)
		if n <= 0 {
			return Ref{}, fmt.Errorf("PR number must be positive: %q", s)
		}
		return Ref{Number: n}, nil
	}
	if m := reShort.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[3])
		if n <= 0 {
			return Ref{}, fmt.Errorf("PR number must be positive in %q", s)
		}
		return Ref{Owner: m[1], Repo: m[2], Number: n}, nil
	}
	if u, err := url.Parse(s); err == nil && u.Host == "github.com" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) >= 4 && parts[2] == "pull" {
			n, err := strconv.Atoi(parts[3])
			if err != nil || n <= 0 {
				return Ref{}, fmt.Errorf("PR number in URL must be a positive int: %q", s)
			}
			return Ref{Owner: parts[0], Repo: parts[1], Number: n}, nil
		}
	}
	return Ref{}, fmt.Errorf("unrecognized PR ref %q (try 1234, owner/repo#1234, or https://github.com/owner/repo/pull/1234)", s)
}
