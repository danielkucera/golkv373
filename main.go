package main

import (
	"github.com/gin-gonic/gin"
	"io"
	"log"
	"net"
	"time"
)

const (
	srvAddr         = "226.2.2.2:2068"
	maxDatagramSize = 1600
	ctrlv1		= "\x54\x46\x36\x7a\x60\x02\x00\x00\x00\x00\x00\x03\x03\x01\x00\x26\x00\x00\x00\x00\x02\x34\xc2"
	ctrlv2		= "\x54\x46\x36\x7a\x60\x02\x00\x00\x00\x00\x00\x03\x03\x01\x00\x26\x00\x00\x00\x00\x0d\x2f\xd8"

)

var curFrame *Frame

type Frame struct {
	Number    int
	Complete  bool
	Data      []byte
	LastChunk int
	Next      *Frame
}

func main() {
	curFrame = &Frame{
		Data: make([]byte, 2*1024*1024),
	}
	go activateStream()
	go serveMulticastUDP(srvAddr, msgHandler)

	router := gin.Default()

	router.GET("/frame.mjpg", func(c *gin.Context) {

		frame := curFrame

		c.Header("Content-Type", "multipart/x-mixed-replace; boundary=--myboundary")

		stopStream := true
		c.Stream(func(w io.Writer) bool {
			defer func() {
				stopStream = false
			}()

			for true {

				for !frame.Complete {
					time.Sleep(10 * time.Millisecond)
				}

				content := append(frame.Data, []byte("\r\n")...)

				_, err := w.Write(append([]byte("--myboundary\r\nContent-Type: image/jpeg\r\n\r\n"), content...))
				if err != nil {
				}

				frame = frame.Next

			}

			return stopStream
		})

	})

	router.GET("/frame.jpeg", func(c *gin.Context) {

		frame := curFrame

		for !frame.Complete {
			time.Sleep(10 * time.Millisecond)
		}

		c.Data(200, "image/jpeg", frame.Data)
	})


	router.GET("/", func(c *gin.Context) {
		c.Data(200, "text/html", []byte("<img src='frame.mjpg'>"))
	})
	router.Run(":8080")
}

func activateStream() {
	addr := net.UDPAddr{
	    Port: 48689,
	    IP:   net.ParseIP("0.0.0.0"),
	}
	conn, err := net.ListenUDP("udp", &addr)
	if err != nil {
	    panic(err)
	}
	defer conn.Close()

	var buf [1024]byte
	for {
	    _, remote, err := conn.ReadFromUDP(buf[:])
	    if err != nil {
		log.Printf(err.Error())
	    }
	    conn.WriteToUDP([]byte(ctrlv2), remote)
	    log.Printf("keepalive sent to %s", remote)
	}

}

func msgHandler(src *net.UDPAddr, n int, b []byte) {
	chunk_n := int(b[2])*256&0xef + int(b[3])
	frame_n := int(b[0])*256 + int(b[1])
	data := b[4:n]
	endframe := (b[2] & 0x80) > 0

	if chunk_n != curFrame.LastChunk + 1 {
		log.Println(frame_n, "was expecting chunk", curFrame.LastChunk + 1, " got", chunk_n)
	}

	if endframe {
		log.Println(n, "end of frame", frame_n)
		curFrame.Next = &Frame{
			Number: frame_n,
			LastChunk: -1,
			Data:   make([]byte, 2*1024*1024),
		}
		curFrame.Data = append(curFrame.Data[:1024*curFrame.LastChunk], data...)
		curFrame.Complete = true
		curFrame = curFrame.Next
	} else {
		copy(curFrame.Data[1020*chunk_n:], data)
		curFrame.LastChunk = chunk_n
	}

	//	log.Println(n, "bytes read from", src, curFrame.Number, chunk_n, endframe)
	//log.Println(hex.Dump(data))
}

func serveMulticastUDP(a string, h func(*net.UDPAddr, int, []byte)) {
	addr, err := net.ResolveUDPAddr("udp", a)
	if err != nil {
		log.Fatal(err)
	}
	l, err := net.ListenMulticastUDP("udp", nil, addr)
	l.SetReadBuffer(50*maxDatagramSize)
	for {
		b := make([]byte, maxDatagramSize)
		n, src, err := l.ReadFromUDP(b)
		if err != nil {
			log.Fatal("ReadFromUDP failed:", err)
		}
		h(src, n, b)
	}
}
