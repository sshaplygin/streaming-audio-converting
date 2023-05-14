package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"runtime/debug"
	"strconv"
	"syscall"
	"time"
)

var fileBuffer bytes.Buffer

const filename = "source.wav"
const name = "source"
const filetype = "audio/wav"

func chunkedHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", filetype)

	// w.WriteHeader(http.StatusOK)
	// w.Header().Set("Content-Disposition", "inline; filename="+filename)
	// w.Header().Set("Accept-Ranges", "bytes")
	// w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(data)-1, len(data)))

	// w.WriteHeader(http.StatusPartialContent)

	buf := make([]byte, 1024)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	flush := w.(http.Flusher)

Loop:
	for {
		select {
		case <-ticker.C:
			// default:
			n, err := fileBuffer.Read(buf)
			if err != nil {
				if errors.Is(err, io.EOF) {
					// End the transfer by sending a zero-length chunk
					w.Write([]byte("0\r\n\r\n"))

					break Loop
				}

				log.Println("read chunk:", err)

				w.WriteHeader(http.StatusInternalServerError)
				return
			}

			log.Println("reader n bytes:", n)

			// Write the size of the chunk in hexadecimal format, followed by a CRLF
			fmt.Fprintf(w, "%x\r\n", n)

			n, err = w.Write(buf)
			if err != nil {
				log.Println("write to client connect:", err)

				w.WriteHeader(http.StatusInternalServerError)
				return
			}

			// Write a CRLF to indicate the end of the chunk
			w.Write([]byte("\r\n"))

			log.Println("writed n bytes:", n)

			flush.Flush()
		}
	}
}

func sendPartialContent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", filetype)

	// Open the file to be sent
	f, err := os.Open(filename)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()

	// Get the range header from the request
	rangeHeader := r.Header.Get("Range")
	if rangeHeader == "" {
		http.Error(w, "Missing Range header", http.StatusBadRequest)
		return
	}

	// Parse the range header to get the starting and ending byte positions
	byteRange, err := parseRangeHeader(rangeHeader, f)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Seek to the starting byte position in the file
	_, err = f.Seek(byteRange.start, 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Set the Content-Length header to the size of the requested range
	w.Header().Set("Content-Length", fmt.Sprintf("%d", byteRange.length))

	fileInfo, err := f.Stat()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Set the Content-Range header to indicate the range of bytes that were returned
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", byteRange.start, byteRange.end, fileInfo.Size()))

	// Set the status code to 206 to indicate a partial response
	w.WriteHeader(http.StatusPartialContent)

	// Copy the requested portion of the file to the response body
	io.CopyN(w, f, byteRange.length)
}

type byteRange struct {
	start  int64
	end    int64
	length int64
}

func parseRangeHeader(rangeHeader string, f *os.File) (*byteRange, error) {
	matches := rangeRegex.FindStringSubmatch(rangeHeader)
	if matches == nil {
		return nil, errors.New("Invalid range header")
	}

	start, err := strconv.ParseInt(matches[1], 10, 64)
	if err != nil {
		return nil, err
	}

	end, err := strconv.ParseInt(matches[2], 10, 64)
	if err != nil {
		return nil, err
	}

	fileSize, err := getFileSize(f)
	if err != nil {
		return nil, err
	}

	if start >= fileSize || end >= fileSize || start > end {
		return nil, errors.New("Invalid range header")
	}

	length := end - start + 1

	return &byteRange{start, end, length}, nil
}

func getFileSize(f *os.File) (int64, error) {
	fileInfo, err := f.Stat()
	if err != nil {
		return 0, err
	}
	return fileInfo.Size(), nil
}

var rangeRegex = regexp.MustCompile(`bytes=(\d+)-(\d+)?`)

func plainTextHandler(w http.ResponseWriter, r *http.Request) {
	// Set response headers
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Transfer-Encoding", "chunked")

	// Start writing the response
	fmt.Fprintf(w, "Start of chunked response\n")

	// Send first chunk
	fmt.Fprintf(w, "This is the first chunk\n")
	w.(http.Flusher).Flush()

	// Wait for a second
	time.Sleep(1 * time.Second)

	// Send second chunk
	fmt.Fprintf(w, "This is the second chunk\n")
	w.(http.Flusher).Flush()

	// Wait for a second
	time.Sleep(1 * time.Second)

	// Send final chunk
	fmt.Fprintf(w, "End of chunked response\n")
	w.(http.Flusher).Flush()
}

func serveContent(w http.ResponseWriter, r *http.Request) {
	file, err := http.Dir(".").Open(filename)
	if err != nil {
		http.Error(w, fmt.Sprintf("unable to open file: %v", err), http.StatusInternalServerError)
		return
	}
	defer file.Close()

	fstat, err := file.Stat()
	if err != nil {
		http.Error(w, fmt.Sprintf("unable to get file stats: %v", err), http.StatusInternalServerError)
		return
	}

	http.ServeContent(w, r, name, fstat.ModTime(), file)
}

func main() {
	ctx, cancel := WithCancel(context.Background())
	defer cancel()

	defer func() {
		if p := recover(); p != nil {
			log.Println("shit happend. recovered panic:", p, string(debug.Stack()))
		}
	}()

	file, err := os.Open(filename)
	if err != nil {
		log.Panic(err)
	}

	n, err := io.Copy(&fileBuffer, file)
	if err != nil {
		log.Panic(err)
	}

	log.Println("read bytes from source file:", n)

	mux := http.NewServeMux()

	mux.HandleFunc("/plain", plainTextHandler)
	mux.HandleFunc("/chunked", chunkedHandler)
	mux.HandleFunc("/partial", sendPartialContent)
	mux.HandleFunc("/serve-content", serveContent)

	server := &http.Server{
		Handler: mux,
		Addr:    "127.0.0.1:18090",
	}

	go func() {
		log.Println("server was run")

		if err := server.ListenAndServe(); err != nil {
			log.Println("server down:", err)
		}
	}()

	<-ctx.Done()

	shutdown(server)

	log.Println("server was shutdown")
}

func shutdown(server *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Println("shutdown: ", err)
	}
}

func WithCancel(parent context.Context) (context.Context, context.CancelFunc) {
	return notifyContext(parent, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
}

func notifyContext(parent context.Context, signals ...os.Signal) (ctx context.Context, stop context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, signals...)
	if ctx.Err() == nil {
		go func() {
			select {
			case <-ch:
				cancel()
			case <-ctx.Done():
			}
		}()
	}
	return ctx, func() {
		cancel()
		signal.Stop(ch)
	}
}
