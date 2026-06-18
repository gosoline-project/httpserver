package httpserver

import (
	"compress/gzip"
	"io"

	"github.com/gin-gonic/gin"
	"github.com/hashicorp/go-multierror"
)

type countingBodyWriter struct {
	gin.ResponseWriter
	writtenBytes int
}

// NewCountingBodyWriter wraps a Gin response writer and returns a counter for written bytes.
func NewCountingBodyWriter(writer gin.ResponseWriter) (responseWriter gin.ResponseWriter, writtenBytes *int) {
	result := &countingBodyWriter{
		ResponseWriter: writer,
		writtenBytes:   0,
	}

	return result, &result.writtenBytes
}

func (r *countingBodyWriter) WriteString(s string) (n int, err error) {
	writtenBytes, err := r.ResponseWriter.WriteString(s)
	r.writtenBytes += writtenBytes

	return writtenBytes, err
}

func (r *countingBodyWriter) Write(p []byte) (n int, err error) {
	writtenBytes, err := r.ResponseWriter.Write(p)
	r.writtenBytes += writtenBytes

	return writtenBytes, err
}

type countingBodyReader struct {
	io.ReadCloser
	readBytes int
}

// NewCountingBodyReader wraps a request body reader and returns a counter for read bytes.
func NewCountingBodyReader(reader io.ReadCloser) (readCloser io.ReadCloser, readBytes *int) {
	result := &countingBodyReader{
		ReadCloser: reader,
		readBytes:  0,
	}

	return result, &result.readBytes
}

func (r *countingBodyReader) Read(p []byte) (int, error) {
	readBytes, err := r.ReadCloser.Read(p)
	r.readBytes += readBytes

	return readBytes, err
}

type gzipBodyReader struct {
	body      io.Closer
	reader    *gzip.Reader
	readBytes int
}

// NewGZipBodyReader wraps a gzip-compressed request body and counts uncompressed bytes.
func NewGZipBodyReader(body io.ReadCloser) (io.ReadCloser, *int, error) {
	reader, err := gzip.NewReader(body)
	result := &gzipBodyReader{
		body:      body,
		reader:    reader,
		readBytes: 0,
	}

	return result, &result.readBytes, err
}

func (r *gzipBodyReader) Read(p []byte) (int, error) {
	readBytes, err := r.reader.Read(p)
	r.readBytes += readBytes

	return readBytes, err
}

func (r *gzipBodyReader) Close() error {
	var result *multierror.Error

	if err := r.reader.Close(); err != nil {
		result = multierror.Append(result, err)
	}

	if err := r.body.Close(); err != nil {
		result = multierror.Append(result, err)
	}

	return result.ErrorOrNil()
}
