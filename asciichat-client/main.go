package main

import (
	"fmt"
	"image"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"time"

	"gocv.io/x/gocv"
	"golang.org/x/term"
)

var asciiChars = []byte(" .:-=+*#%@")

func main() {
	if runtime.GOOS != "darwin" {
		fmt.Fprintln(os.Stderr, "This program is intended for macOS")
		os.Exit(1)
	}

	// Handle Ctrl+C gracefully
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		<-c
		fmt.Print("\033[?25h")   // show cursor
		fmt.Print("\033[0m")     // reset colors
		fmt.Print("\033[?1049l") // exit alt screen
		os.Exit(0)
	}()

	// Open GoCV webcam
	webcam, err := gocv.OpenVideoCapture(1)
	if err != nil || !webcam.IsOpened() {
		panic("Unable to open webcam")
	}
	defer webcam.Close()

	// Alt screen + hide cursor
	fmt.Print("\033[?1049h") // alt screen
	fmt.Print("\033[?25l")   // hide cursor
	defer func() {
		fmt.Print("\033[?25h")   // show cursor
		fmt.Print("\033[?1049l") // exit alt screen
	}()

	// Detect terminal size
	width, height := 80, 40
	if term.IsTerminal(int(os.Stdout.Fd())) {
		if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
			width = w - 1
			height = h - 1
		}
	}

	// Create Mat for webcam frames
	img := gocv.NewMat()
	defer img.Close()

	var last time.Time
	fps := 0

	for {
		if ok := webcam.Read(&img); !ok || img.Empty() {
			continue
		}

		// Detect terminal size
		// width, height := 80, 40
		if term.IsTerminal(int(os.Stdout.Fd())) {
			if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
				width = w - 1
				height = h - 1
			}
		}

		// Resize frame to terminal size using image.Point

		flipped := gocv.NewMat()
		gocv.Flip(img, &flipped, 1)

		resized := gocv.NewMat()
		gocv.Resize(flipped, &resized, image.Point{X: width, Y: height * 2}, 0, 0, gocv.InterpolationArea)

		ascii := matToASCII(resized)

		flipped.Close()
		resized.Close()

		// Move cursor to top-left and print
		fmt.Print("\033[H")
		fmt.Print(ascii)

		// Show FPS
		fps++
		if time.Since(last) >= time.Second {
			fmt.Printf("\033[%d;1HFPS: %d\n", height, fps)
			fps = 0
			last = time.Now()
		}

		// Limit FPS (~30)
		time.Sleep(33 * time.Millisecond)
	}
}

func matToASCII(mat gocv.Mat) string {
	rows, cols := mat.Rows(), mat.Cols()
	out := make([]byte, 0, rows*cols/2)

	for y := 0; y < rows; y += 2 { // skip every other row for terminal aspect
		for x := 0; x < cols; x++ {
			c := mat.GetVecbAt(y, x) // BGR
			lum := 0.0722*float64(c[0]) + 0.7152*float64(c[1]) + 0.2126*float64(c[2])
			idx := int(lum / 256 * float64(len(asciiChars)-1))
			out = append(out, asciiChars[idx])
		}
		out = append(out, '\n')
	}
	return string(out)
}

func matToASCIIColor(mat gocv.Mat) string {
	rows, cols := mat.Rows(), mat.Cols()

	var b strings.Builder
	b.Grow(rows * cols * 10) // avoid reallocs

	for y := 0; y < rows; y += 2 {
		for x := 0; x < cols; x++ {
			c := mat.GetVecbAt(y, x) // BGR

			bb := c[0]
			gg := c[1]
			rr := c[2]

			// luminance → ascii
			lum := 0.0722*float64(bb) + 0.7152*float64(gg) + 0.2126*float64(rr)
			idx := int(lum / 256 * float64(len(asciiChars)-1))
			ch := asciiChars[idx]

			// 24-bit foreground color
			fmt.Fprintf(&b, "\033[38;2;%d;%d;%dm%c", rr, gg, bb, ch)
		}
		b.WriteByte('\n')
	}

	b.WriteString("\033[0m") // reset color
	return b.String()
}
