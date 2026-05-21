// wavdiff — diff two stereo int16 PCM WAVs sample-by-sample. Reports
// length diff, identical-prefix length, mean/max absolute diff, and a
// rough histogram of per-sample divergence.
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
)

func main() {
	a := flag.String("a", "", "first WAV")
	b := flag.String("b", "", "second WAV")
	dumpFirst := flag.Int("dump-first-diffs", 16, "dump the first N differing samples")
	flag.Parse()
	if *a == "" || *b == "" {
		fmt.Fprintln(os.Stderr, "usage: wavdiff -a A.wav -b B.wav [-dump-first-diffs N]")
		os.Exit(2)
	}
	aSamples, aRate, err := readWAV(*a)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read %s: %v\n", *a, err)
		os.Exit(1)
	}
	bSamples, bRate, err := readWAV(*b)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read %s: %v\n", *b, err)
		os.Exit(1)
	}

	fmt.Printf("a: %d samples @ %d Hz (%d stereo frames, %.2fs)\n",
		len(aSamples), aRate, len(aSamples)/2, float64(len(aSamples)/2)/float64(aRate))
	fmt.Printf("b: %d samples @ %d Hz (%d stereo frames, %.2fs)\n",
		len(bSamples), bRate, len(bSamples)/2, float64(len(bSamples)/2)/float64(bRate))
	if aRate != bRate {
		fmt.Println("sample rates differ — skipping per-sample diff")
		return
	}

	n := min(len(aSamples), len(bSamples))
	identical := 0
	for i := 0; i < n; i++ {
		if aSamples[i] != bSamples[i] {
			break
		}
		identical++
	}
	fmt.Printf("identical-prefix: %d samples (%.2f%% of shared length)\n",
		identical, 100*float64(identical)/float64(n))

	diffs := 0
	var sumAbs int64
	maxAbs := int32(0)
	hist := [5]int{} // |diff| in [0,1] [2,16] [17,256] [257,4096] [4097,32768]
	for i := 0; i < n; i++ {
		d := int32(aSamples[i]) - int32(bSamples[i])
		ad := abs32(d)
		if ad > 0 {
			diffs++
			sumAbs += int64(ad)
			if ad > maxAbs {
				maxAbs = ad
			}
			switch {
			case ad <= 1:
				hist[0]++
			case ad <= 16:
				hist[1]++
			case ad <= 256:
				hist[2]++
			case ad <= 4096:
				hist[3]++
			default:
				hist[4]++
			}
		}
	}
	fmt.Printf("diverging samples: %d / %d (%.2f%%)\n",
		diffs, n, 100*float64(diffs)/float64(n))
	if diffs > 0 {
		fmt.Printf("mean |diff|: %.2f, max |diff|: %d\n",
			float64(sumAbs)/float64(diffs), maxAbs)
		buckets := []string{"|d|=1", "|d|≤16", "|d|≤256", "|d|≤4k", "|d|≤32k"}
		for i, c := range hist {
			fmt.Printf("  %s: %d\n", buckets[i], c)
		}
	}

	// Length divergence: where does the longer file start having
	// data the other doesn't?
	if len(aSamples) != len(bSamples) {
		shorter, longer := len(aSamples), len(bSamples)
		whichLonger := "b"
		if len(aSamples) > len(bSamples) {
			shorter, longer = len(bSamples), len(aSamples)
			whichLonger = "a"
		}
		fmt.Printf("%s is longer by %d samples (%d extra stereo frames, %.3fs)\n",
			whichLonger, longer-shorter, (longer-shorter)/2,
			float64((longer-shorter)/2)/float64(aRate))
	}

	if *dumpFirst > 0 && diffs > 0 {
		fmt.Println("first diffs:")
		dumped := 0
		for i := 0; i < n && dumped < *dumpFirst; i++ {
			if aSamples[i] != bSamples[i] {
				fmt.Printf("  [%d] a=%d b=%d (Δ=%d)\n", i,
					aSamples[i], bSamples[i],
					int32(aSamples[i])-int32(bSamples[i]))
				dumped++
			}
		}
	}

	// Compute RMS as a perceptual signal level for context.
	var sumSq float64
	for _, s := range aSamples {
		sumSq += float64(s) * float64(s)
	}
	rms := math.Sqrt(sumSq / float64(len(aSamples)))
	fmt.Printf("a RMS: %.0f (%.1f dBFS)\n", rms, 20*math.Log10(rms/32768))
}

func abs32(x int32) int32 {
	if x < 0 {
		return -x
	}
	return x
}

func readWAV(path string) ([]int16, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	buf := make([]byte, 44)
	if _, err := io.ReadFull(f, buf); err != nil {
		return nil, 0, fmt.Errorf("header: %w", err)
	}
	if string(buf[0:4]) != "RIFF" || string(buf[8:12]) != "WAVE" {
		return nil, 0, fmt.Errorf("not a RIFF/WAVE file")
	}
	rate := int(binary.LittleEndian.Uint32(buf[24:28]))
	rest, err := io.ReadAll(f)
	if err != nil {
		return nil, 0, err
	}
	if len(rest)%2 != 0 {
		return nil, 0, fmt.Errorf("odd byte count")
	}
	samples := make([]int16, len(rest)/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(rest[i*2:]))
	}
	return samples, rate, nil
}
