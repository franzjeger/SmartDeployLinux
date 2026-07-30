// images subcommands: list images, upload a new image version.
//
// Upload is the headless counterpart to the UI's version panel:
// stream-hash the file, register the blob, PUT the bytes straight to
// the object store via the presigned URL, then link the version.

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"text/tabwriter"
	"time"

	"github.com/your-org/deployserver/deployctl/internal/client"
)

func imagesMain(args []string) {
	if len(args) < 1 {
		imagesUsage()
		os.Exit(2)
	}
	switch args[0] {
	case "list":
		imagesList()
	case "upload":
		imagesUpload(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown images command: %s\n", args[0])
		imagesUsage()
		os.Exit(2)
	}
}

func imagesUsage() {
	fmt.Fprint(os.Stderr, `Usage:
  deployctl images list
  deployctl images upload --image <image-id> --file <path> [--tag <version-tag>]
`)
}

func imagesList() {
	c := mustClient()
	var images []struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		OSFamily      string `json:"os_family"`
		OSVersion     string `json:"os_version"`
		Arch          string `json:"arch"`
		VersionsCount int    `json:"versions_count"`
	}
	if err := c.Do("GET", "/api/v1/images", nil, &images); err != nil {
		fatal(err)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tOS\tARCH\tVERSIONS")
	for _, im := range images {
		fmt.Fprintf(tw, "%s\t%s\t%s %s\t%s\t%d\n",
			im.ID, im.Name, im.OSFamily, im.OSVersion, im.Arch, im.VersionsCount)
	}
	tw.Flush()
}

func imagesUpload(args []string) {
	fs := flag.NewFlagSet("images upload", flag.ExitOnError)
	imageID := fs.String("image", "", "target image id (required)")
	file := fs.String("file", "", "path to the image payload, e.g. install.wim (required)")
	tag := fs.String("tag", "", "version tag (default: server-assigned timestamp)")
	_ = fs.Parse(args)
	if *imageID == "" || *file == "" {
		imagesUsage()
		os.Exit(2)
	}
	c := mustClient()

	f, err := os.Open(*file)
	if err != nil {
		fatal(err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		fatal(err)
	}

	fmt.Fprintf(os.Stderr, "hashing %s (%.1f MiB)…\n", *file, float64(st.Size())/(1<<20))
	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		fatal(err)
	}
	sha := hex.EncodeToString(hasher.Sum(nil))

	var blob struct {
		BlobID    string `json:"blob_id"`
		UploadURL string `json:"upload_url"`
	}
	if err := c.Do("POST", "/api/v1/blobs", map[string]any{
		"sha256":     sha,
		"size_bytes": st.Size(),
		"filename":   st.Name(),
		"role":       "images",
	}, &blob); err != nil {
		fatal(err)
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		fatal(err)
	}
	fmt.Fprintln(os.Stderr, "uploading to object store…")
	req, err := http.NewRequest(http.MethodPut, blob.UploadURL, f)
	if err != nil {
		fatal(err)
	}
	req.ContentLength = st.Size()
	// Uploads can run for hours; no client timeout, rely on TCP.
	up := &http.Client{Timeout: 0, Transport: c.HTTP.Transport}
	start := time.Now()
	resp, err := up.Do(req)
	if err != nil {
		fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		fatal(fmt.Errorf("upload failed: HTTP %d: %s", resp.StatusCode, string(b)))
	}
	fmt.Fprintf(os.Stderr, "uploaded in %s\n", time.Since(start).Round(time.Second))

	var out struct {
		VersionID  string `json:"version_id"`
		VersionTag string `json:"version_tag"`
	}
	if err := c.Do("POST", "/api/v1/images/"+*imageID+"/versions", map[string]any{
		"blob_id":     blob.BlobID,
		"version_tag": *tag,
	}, &out); err != nil {
		fatal(err)
	}
	fmt.Printf("version %s (%s) registered on image %s\n", out.VersionTag, out.VersionID, *imageID)
}

func mustClient() *client.Client {
	c, err := client.New()
	if err != nil {
		fatal(err)
	}
	return c
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
