package main

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"io"
	"log"
	"net"
	"time"
)

const (
	srvAddr         = "226.2.2.2:2068"
	maxDatagramSize = 1600
	ctrlv1          = "\x54\x46\x36\x7a\x60\x02\x00\x00\x00\x00\x00\x03\x03\x01\x00\x26\x00\x00\x00\x00\x02\x34\xc2"
	ctrlv2          = "\x54\x46\x36\x7a\x60\x02\x00\x00\x00\x00\x00\x03\x03\x01\x00\x26\x00\x00\x00\x00\x0d\x2f\xd8"
)

var recvMap map[string]*Frame

type Frame struct {
	Number    int
	Complete  bool
	Data      []byte `json:"-"`
	LastChunk int
	Next      *Frame
}

func main() {
	recvMap = make(map[string]*Frame)
	go activateStream()
	go serveMulticastUDP(srvAddr, msgHandler)

	router := gin.Default()

	dev := router.Group("/src/:IP", func(c *gin.Context) {
		IP := c.Param("IP")

		if IP == "default" {
			log.Println("Handling default")
			for key := range recvMap {
				IP = key
				log.Println("Setting to ", key)
				break
			}
		}

		if _, ok := recvMap[IP]; ok {
			c.Set("IP", IP)
		} else {
			c.String(404, "Device not found")
			c.Abort()
		}
	})

	{
		dev.GET("/frame.mjpg", func(c *gin.Context) {
			IP := c.MustGet("IP").(string)
			frame := recvMap[IP]

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

		dev.GET("/frame.jpeg", func(c *gin.Context) {
			IP := c.MustGet("IP").(string)
			frame := recvMap[IP]

			for !frame.Complete {
				time.Sleep(10 * time.Millisecond)
			}

			c.Data(200, "image/jpeg", frame.Data)
		})

		dev.GET("/", func(c *gin.Context) {
			c.Data(200, "text/html", []byte("<img src='frame.mjpg'>"))
		})
	}

	//TODO: proper status page
	router.GET("/status", func(c *gin.Context) {
		c.JSON(200, recvMap)
	})

	//TODO: redesign
	router.GET("/", func(c *gin.Context) {
		html := "<h2>Available streams</h2>\n<ul>\n"
		html += "<li><a href='src/default/'>default</a>\n"
		for key := range recvMap {
			html += "<li><a href='src/" + key + "'>" + key + "</a>\n"
		}
		html += "</ul>\n"
		html += "<h2>Status</h2>\n"
		status, _ := json.MarshalIndent(recvMap, "", "\t")
		html += "<pre>" + string(status) + "</pre>"
		c.Header("Content-Type", "text/html")
		c.String(200, html)
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
	IP := src.IP.String()

	if _, ok := recvMap[IP]; !ok {
		recvMap[IP] = &Frame{
			Number:    frame_n,
			LastChunk: -1,
			Data:      make([]byte, 2*1024*1024),
		}
	}

	curFrame := recvMap[IP]

	if chunk_n != curFrame.LastChunk+1 {
		log.Println(frame_n, "was expecting chunk", curFrame.LastChunk+1, " got", chunk_n)
	}

	if endframe {
		//log.Println(n, "end of frame", frame_n)
		curFrame.Next = &Frame{
			Number:    frame_n,
			LastChunk: -1,
			Data:      make([]byte, 2*1024*1024),
		}
		curLen := 1020*(curFrame.LastChunk+1)
		curFrame.Data = append(curFrame.Data[:curLen], data...)
		curFrame.Complete = true
		recvMap[IP] = curFrame.Next
	} else {
		copy(curFrame.Data[1020*chunk_n:], data)
		curFrame.LastChunk = chunk_n
	}

	//	log.Println(n, "bytes read from", src, curFrame.Number, chunk_n, endframe)
	//log.Println(curFrame)
	//log.Println(hex.Dump(data))
}

func serveMulticastUDP(a string, h func(*net.UDPAddr, int, []byte)) {
	addr, err := net.ResolveUDPAddr("udp", a)
	if err != nil {
		log.Fatal(err)
	}
	l, err := net.ListenMulticastUDP("udp", nil, addr)
	l.SetReadBuffer(2*1024*1024)
	b := make([]byte, maxDatagramSize)
	for {
		n, src, err := l.ReadFromUDP(b)
		if err != nil {
			log.Fatal("ReadFromUDP failed:", err)
		}
		h(src, n, b)
	}
}
