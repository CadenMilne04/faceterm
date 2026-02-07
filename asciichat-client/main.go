package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"log"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"gocv.io/x/gocv"
	"golang.org/x/term"
)

// connectWS connects to the given ws:// or wss:// URL and returns the connection
func connectWS(addr string) *websocket.Conn {
	u := url.URL{Scheme: "wss", Host: addr, Path: "/ws"}
	log.Printf("connecting to %s", u.String())

	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Fatalf("failed to connect to websocket: %v", err)
	}
	return c
}

var asciiChars = []byte(" .:-=+*#%@")

type MessageType string

const (
	MsgTypeSize  MessageType = "size"
	MsgTypeFrame MessageType = "frame"
)

const serverAddress = "asciichat.cadenmilne.com"

type Message struct {
	Type   MessageType `json:"type"`
	Width  int         `json:"width,omitempty"`
	Height int         `json:"height,omitempty"`
	Frame  string      `json:"frame,omitempty"`
}

func processFrame(img gocv.Mat, width, height int, color bool) string {
	// Flip horizontally (mirror)
	flipped := gocv.NewMat()
	gocv.Flip(img, &flipped, 1)
	defer flipped.Close()

	// Resize to terminal size (*2 for aspect correction)
	resized := gocv.NewMat()
	gocv.Resize(flipped, &resized, image.Point{X: width, Y: height * 2}, 0, 0, gocv.InterpolationArea)
	defer resized.Close()

	// Convert to ASCII
	var ascii string
	if color {
		ascii = matToASCIIColor(resized)
	} else {
		ascii = matToASCII(resized)
	}

	return ascii
}

func sendTerminalSize(ws *websocket.Conn, width, height int) {
	msg := Message{
		Type:   MsgTypeSize,
		Width:  width,
		Height: height,
	}
	b, _ := json.Marshal(msg)
	if err := ws.WriteMessage(websocket.TextMessage, b); err != nil {
		log.Println("write size error:", err)
	}
}

var latestRemoteFrame atomic.Value // stores string

func main() {

	// Handle cli args
	device := flag.Int("device", -1, "A device number from ffmpeg's list")
	color := flag.Bool("color", false, "Use color or not?")
	flag.Parse()

	// Check required integer flags
	if *device == -1 {
		fmt.Fprintln(os.Stderr, "Error: -device flag is required")
		flag.Usage()
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

	ws := connectWS(serverAddress)
	defer ws.Close()

	// Open GoCV webcam
	webcam, err := gocv.OpenVideoCapture(*device)
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

	// Initialize terminal size as this clients size, later it will update w + h from other client
	width, height := 80, 40
	if term.IsTerminal(int(os.Stdout.Fd())) {
		if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
			width = w - 1
			height = h - 1
		}
	}

	// channel for frames to send
	// frameChannel := make(chan string)

	// goroutine: continuously read messages from WS
	go func() {
		for {
			_, data, err := ws.ReadMessage()
			if err != nil {
				log.Println("read error:", err)
				return
			}

			var msg Message
			if err := json.Unmarshal(data, &msg); err != nil {
				log.Println("json unmarshal error:", err)
				continue
			}

			switch msg.Type {
			case MsgTypeFrame:
				// move cursor to top-left
				print("\033[H")
				print(msg.Frame)
			case MsgTypeSize:
				// handle remote terminal size
				width = msg.Width
				height = msg.Height
			}
		}
	}()

	// goroutine: continuously send frames from frameCh
	// go func() {
	// 	for f := range frameChannel {
	// 		msg := Message{
	// 			Type:  "frame",
	// 			Frame: f,
	// 		}
	// 		b, _ := json.Marshal(msg)
	// 		if err := ws.WriteMessage(websocket.TextMessage, b); err != nil {
	// 			log.Println("write error:", err)
	// 			return
	// 		}
	// 	}
	// }()

	// goroutine: periodically send terminal size
	// go func() {
	// 	var lastW, lastH int
	// 	for {
	// 		if term.IsTerminal(int(os.Stdout.Fd())) {
	// 			w, h, err := term.GetSize(int(os.Stdout.Fd()))
	// 			if err == nil {
	// 				if w != lastW || h != lastH {
	// 					sendTerminalSize(ws, w-1, h-1) // minus 1 to match your frame resizing
	// 					lastW, lastH = w, h
	// 				}
	// 			}
	// 		}
	// 		time.Sleep(500 * time.Millisecond) // adjust frequency as needed
	// 	}
	// }()

	// Create Mat for webcam frames
	img := gocv.NewMat()
	defer img.Close()

	lastW, lastH := width, height // initialize
	for {
		if ok := webcam.Read(&img); !ok || img.Empty() {
			continue
		}

		// Get current terminal size
		if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
			width = w - 1
			height = h - 1
		}

		// Prepare messages
		msgs := []Message{
			{Type: MsgTypeFrame, Frame: processFrame(img, width, height, *color)},
		}

		// Only send terminal size if changed
		if width != lastW || height != lastH {
			msgs = append(msgs, Message{Type: MsgTypeSize, Width: width, Height: height})
			lastW, lastH = width, height
		}

		// Send all messages sequentially (single goroutine)
		for _, msg := range msgs {
			b, _ := json.Marshal(msg)
			if err := ws.WriteMessage(websocket.TextMessage, b); err != nil {
				log.Println("write error:", err)
				return
			}
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
