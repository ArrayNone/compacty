package compressor

import (
	"bytes"
	"fmt"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/ArrayNone/compacty/internal/textutils"
	"github.com/gabriel-vasile/mimetype"
)

type DecodeTimeBench struct {
	Total   time.Duration
	Average time.Duration
	Trials  int

	Err error
}

func (dt DecodeTimeBench) MSAverageToString() string {
	if dt.Err != nil {
		return "ERROR: " + dt.Err.Error()
	} else if dt.Trials == 0 || dt.Total == 0 {
		return "-"
	}

	const nanoPerMilli = 1000000
	decodeMillisecondsAverage := float64(dt.Total.Nanoseconds()) / float64(nanoPerMilli*dt.Trials)
	return strconv.FormatFloat(decodeMillisecondsAverage, 'f', 6, 64)
}

func (dt DecodeTimeBench) MSAverageWithTrialsCountString() string {
	if dt.Trials == 0 {
		return dt.MSAverageToString()
	}

	trialText := textutils.PluralNoun(dt.Trials, "trials", "trial")
	return fmt.Sprintf("%s (%d %s)", dt.MSAverageToString(), dt.Trials, trialText)
}

func benchDecodeTime(filePath string, minTime time.Duration) (result DecodeTimeBench) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return DecodeTimeBench{Err: err}
	}

	reader := bytes.NewReader(data)
	mimeType, err := mimetype.DetectReader(reader)
	if err != nil {
		return DecodeTimeBench{Err: err}
	}

	decodeFunc, ok := getDecodeFunc(mimeType)
	if !ok {
		return DecodeTimeBench{}
	}

	_, _ = reader.Seek(0, io.SeekStart)
	err = decodeFunc(reader)
	if err != nil { // Minimise interference by only checking once
		return DecodeTimeBench{Err: err}
	}

	var totalTime time.Duration
	var trials int

	start := time.Now()
	for totalTime < minTime {
		_, _ = reader.Seek(0, io.SeekStart)
		_ = decodeFunc(reader)

		totalTime = time.Since(start)

		trials++
	}

	return DecodeTimeBench{Total: totalTime, Average: totalTime / time.Duration(trials), Trials: trials}
}

func getDecodeFunc(mime *mimetype.MIME) (decodeFunc func(io.Reader) error, ok bool) {
	switch {
	case mime.Is("image/png"):
		return func(r io.Reader) error {
			_, err := png.Decode(r)
			return err
		}, true
	case mime.Is("image/jpeg"):
		return func(r io.Reader) error {
			_, err := jpeg.Decode(r)
			return err
		}, true
	case mime.Is("image/gif"):
		return func(r io.Reader) error {
			_, err := gif.Decode(r)
			return err
		}, true
	}

	return nil, false
}
