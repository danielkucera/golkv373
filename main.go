package main

import (
	"github.com/gin-gonic/gin"
	"log"
	"net"
	"time"
)

const (
	srvAddr         = "226.2.2.2:2068"
	txAddr          = "192.168.168.55:48689"
	maxDatagramSize = 8192
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
	go activateStream(txAddr)
	go serveMulticastUDP(srvAddr, msgHandler)

	router := gin.Default()

	router.GET("/frame.mjpg", func(c *gin.Context) {

		frame := curFrame

		for true {

			for !frame.Complete {
				time.Sleep(10 * time.Millisecond)
			}

			content := append(frame.Data, []byte("\n")...)

			c.Data(200, "video/x-motion-jpeg", append([]byte("--myboundary\nContent-Type: image/jpeg\n"), content...))

			frame = frame.Next

		}
	})

	router.GET("/", func(c *gin.Context) {
		c.Data(200, "text/html", []byte("<img src='frame.mjpg'>"))
	})
	router.Run(":8080")
}

func activateStream(a string) {
	addr, err := net.ResolveUDPAddr("udp", a)
	if err != nil {
		log.Fatal(err)
	}
	c, err := net.DialUDP("udp", nil, addr)
	for {
		c.Write([]byte{0x54, 0x46, 0x36, 0x7A, 0x60, 0x02, 0x00, 0x00, 0x25, 0x14, 0x00, 0x03, 0x03, 0x01, 0x00, 0x26,
		0x1f, 0x1f, 0x00, 0x00, 0x4e, 0xa5, 0x03})
		time.Sleep(1 * time.Second)
		log.Printf("keepalive sent")
	}
}

func msgHandler(src *net.UDPAddr, n int, b []byte) {
	chunk_n := int(b[2])*256&0xef + int(b[3])
	frame_n := int(b[0])*256 + int(b[1])
	data := b[4:]
	endframe := (b[2] & 0x80) > 0

	if frame_n != curFrame.Number {
	}

	if endframe {
		log.Println(n, "end of frame", frame_n)
		curFrame.Next = &Frame{
			Number: frame_n,
			Data:   make([]byte, 2*1024*1024),
		}
		curFrame.Data = append(curFrame.Data[:1024*curFrame.LastChunk], data...)
		curFrame.Complete = true
		curFrame = curFrame.Next
	} else {
		copy(curFrame.Data[1024*chunk_n:], data)
		curFrame.LastChunk = chunk_n
	}

	//	log.Println(n, "bytes read from", src, curFrame.Number, chunk_n, endframe)
	//log.Println(hex.Dump(b[:n]))
}

func serveMulticastUDP(a string, h func(*net.UDPAddr, int, []byte)) {
	addr, err := net.ResolveUDPAddr("udp", a)
	if err != nil {
		log.Fatal(err)
	}
	l, err := net.ListenMulticastUDP("udp", nil, addr)
	l.SetReadBuffer(maxDatagramSize)
	for {
		b := make([]byte, maxDatagramSize)
		n, src, err := l.ReadFromUDP(b)
		if err != nil {
			log.Fatal("ReadFromUDP failed:", err)
		}
		h(src, n, b)
	}
}
