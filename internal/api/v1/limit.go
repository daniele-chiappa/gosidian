package v1

import (
	"net/http"
	"strconv"
	"strings"
)

// limitParam reads the "limit" query parameter: def when it is absent, not a
// number or not positive, maxLimit when it is larger. A larger value used to
// fall back to def on some endpoints, so asking for more returned fewer
// (BUG-075).
func limitParam(req *http.Request, def, maxLimit int) int {
	limit, err := strconv.Atoi(strings.TrimSpace(req.URL.Query().Get("limit")))
	if err != nil || limit <= 0 {
		return def
	}
	return min(limit, maxLimit)
}
