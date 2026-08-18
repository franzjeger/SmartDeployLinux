package main

import (
	"os"
	"regexp"
	"strings"
)

// mirrorCacheRe matches the host of an http:// URL.
var mirrorCacheRe = regexp.MustCompile(`http://([^/ "']+)/`)

// cacheRewrite routes http:// mirror URLs through the local caching proxy
// (MIRROR_CACHE_BASE, e.g. http://host:8080/cache) so a distro's
// kernel/initrd/repo is fetched from the internet once and served from
// local disk on every later deploy. No-op when MIRROR_CACHE_BASE is unset.
func cacheRewrite(s string) string {
	base := os.Getenv("MIRROR_CACHE_BASE")
	if base == "" {
		return s
	}
	base = strings.TrimRight(base, "/")
	return mirrorCacheRe.ReplaceAllString(s, base+"/$1/")
}
