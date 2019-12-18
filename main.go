package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"image"
	"image/jpeg"
	"io"
	"log"
	"mime/multipart"
	"net"
	"net/textproto"
	"os"
	"strconv"
	"time"
)

const (
	srvAddr         = "226.2.2.2:2068"
	maxDatagramSize = 1600
	ctrlv1          = "\x54\x46\x36\x7a\x60\x02\x00\x00\x00\x00\x00\x03\x03\x01\x00\x26\x00\x00\x00\x00\x02\x34\xc2"
	ctrlv2          = "\x54\x46\x36\x7a\x60\x02\x00\x00\x00\x00\x00\x03\x03\x01\x00\x26\x00\x00\x00\x00\x0d\x2f\xd8"
)

var devices map[string]*Device

type Device struct {
	Frame         *Frame
	FrameConfig   image.Config
	LastFrameTime time.Time
	RxBytes       int
	RxBytesLast   int
	RxFrames      int
	RxFramesLast  int
	ChunksLost    int
	FPS           float32
	BPS           float32
}

type Frame struct {
	Number    int
	Complete  bool
	Damaged   bool
	Data      []byte `json:"-"`
	LastChunk int
	Next      *Frame
}

func (f *Frame) waitComplete(ms int) error {
	for i := 0; i < ms || ms == 0; i++ {
		if f.Complete {
			return nil
		}
		time.Sleep(time.Millisecond)
	}
	return errors.New("Waiting for frame timed out")
}

func main() {
	dolog, _ := strconv.ParseBool(os.Getenv("GOLKV_LOG"))
	listen_string, listen_ok := os.LookupEnv("GOLKV_LISTEN")
	if !listen_ok {
		listen_string = ":8080"
	}

	if dolog {
		t := time.Now()
		logName := fmt.Sprintf("log-%d_%02d_%02d-%02d_%02d_%02d.txt",
			t.Year(), t.Month(), t.Day(),
			t.Hour(), t.Minute(), t.Second())
		logFile, err := os.OpenFile(logName, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0666)
		if err != nil {
			panic(err)
		}

		mw := io.MultiWriter(os.Stdout, logFile)
		log.SetOutput(mw)
		gin.DefaultWriter = mw
	}

	log.Println("Program started as: ", os.Args)

	devices = make(map[string]*Device)
	go activateStream()
	go serveMulticastUDP(srvAddr, msgHandler)
	go statistics()

	router := gin.Default()

	dev := router.Group("/src/:IP", func(c *gin.Context) {
		IP := c.Param("IP")

		if IP == "default" {
			log.Println("Handling default")
			for key := range devices {
				IP = key
				log.Println("Setting to ", key)
				break
			}
		}

		if _, ok := devices[IP]; ok {
			c.Set("IP", IP)
		} else {
			c.String(404, "Device not found")
			c.Abort()
		}
	})

	{
		dev.GET("/frame.mjpg", func(c *gin.Context) {
			var ifd time.Duration

			IP := c.MustGet("IP").(string)
			frame := devices[IP].Frame
			if frame == nil {
				c.String(404, "No frames received")
				c.Abort()
				return
			}

			fps, err := strconv.Atoi(c.DefaultQuery("fps", "0"))
			if fps > 0 && err == nil {
				log.Printf("Client requested %d FPS", fps)
				ifd = time.Duration(1000/fps) * time.Millisecond
			}

			_, rw, err := c.Writer.Hijack()
			bodyWriter := multipart.NewWriter(rw)

			rw.Write([]byte("HTTP/1.1 200 OK\r\n"))
			rw.Write([]byte("Content-Type: multipart/x-mixed-replace; boundary=" + bodyWriter.Boundary() + "\r\n\r\n"))

			for true {

				frame.waitComplete(0)

				if !frame.Damaged {
					mh := make(textproto.MIMEHeader)
					mh.Set("Content-Type", "image/jpeg")
					mh.Set("Content-Length", fmt.Sprintf("%d", len(frame.Data)))

					pw, err := bodyWriter.CreatePart(mh)
					if err != nil {
						return
					}
					_, err = pw.Write(frame.Data)
					if err != nil {
						return
					}
				}

				if ifd > 0 {
					time.Sleep(ifd)
					frame = devices[IP].Frame
				} else {
					frame = frame.Next
				}

			}

			return
		})

		dev.GET("/frame.jpeg", func(c *gin.Context) {
			IP := c.MustGet("IP").(string)
			frame := devices[IP].Frame
			if frame == nil {
				c.String(404, "No frames received")
				c.Abort()
				return
			}

			frame.waitComplete(1000)

			c.Data(200, "image/jpeg", frame.Data)
		})

		dev.GET("/", func(c *gin.Context) {
			c.Data(200, "text/html", []byte("<img src='frame.mjpg'>"))
		})
	}

	//TODO: proper status page
	router.GET("/status", func(c *gin.Context) {
		c.JSON(200, devices)
	})

	//TODO: redesign
	router.GET("/", func(c *gin.Context) {
		html := "<h2>Available streams</h2>\n<ul>\n"
		html += "<li><a href='src/default/'>default</a>\n"
		for key := range devices {
			html += "<li><a href='src/" + key + "'>" + key + "</a>\n"
		}
		html += "</ul>\n"
		html += "<h2>Status</h2>\n"
		status, _ := json.MarshalIndent(devices, "", "\t")
		html += "<pre>" + string(status) + "</pre>"
		c.Header("Content-Type", "text/html")
		c.String(200, html)
	})

	router.Run(listen_string)
}

func statistics() {
	for true {
		active := 0
		for IP := range devices {
			device := devices[IP]
			device.BPS = float32(device.RxBytes - device.RxBytesLast)
			device.FPS = float32(device.RxFrames - device.RxFramesLast)
			device.RxBytesLast = device.RxBytes
			device.RxFramesLast = device.RxFrames
			if device.BPS > 0 {
				log.Printf("%s: MBPS=%d FPS=%d lost=%d", IP, device.BPS/(1024*1024), device.FPS, device.ChunksLost)
				active += 1
			}
			go func(frame *Frame) {
				frame.waitComplete(1000)
				device.FrameConfig, _ = jpeg.DecodeConfig(bytes.NewReader(frame.Data))
			}(device.Frame)
		}
		if active == 0 {
			log.Printf("No active transmitters")
		}
		time.Sleep(time.Second)
	}
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
	chunk_n := (int(b[2])&0x7f)*256 + int(b[3])
	frame_n := int(b[0])*256 + int(b[1])
	data := b[4:n]
	endframe := (b[2] & 0x80) > 0
	IP := src.IP.String()

	if _, ok := devices[IP]; !ok {
		devices[IP] = &Device{}
	}

	device := devices[IP]

	if device.Frame == nil {

		device.Frame = &Frame{
			Number:    frame_n,
			LastChunk: -1,
			Data:      make([]byte, 2*1024*1024),
			Complete:  true,
		}
	}

	if device.Frame.Next == nil {

		device.Frame.Next = &Frame{
			Number:    frame_n,
			LastChunk: -1,
			Data:      make([]byte, 2*1024*1024),
		}
	}

	curFrame := device.Frame.Next

	if chunk_n != curFrame.LastChunk+1 {
		log.Println(frame_n, "was expecting chunk", curFrame.LastChunk+1, " got", chunk_n)
		curFrame.Damaged = true
		device.ChunksLost += chunk_n - curFrame.LastChunk + 1
	}

	device.RxBytes += len(data)

	if endframe {
		//log.Println(n, "end of frame", frame_n)
		curFrame.Next = &Frame{
			Number:    frame_n,
			LastChunk: -1,
			Data:      make([]byte, 2*1024*1024),
		}
		curLen := 1020 * (curFrame.LastChunk + 1)
		curFrame.Data = append(curFrame.Data[:curLen], data...)
		curFrame.Complete = true
		device.RxFrames++
		device.LastFrameTime = time.Now()
		device.Frame = curFrame
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
	l.SetReadBuffer(2 * 1024 * 1024)
	b := make([]byte, maxDatagramSize)
	for {
		n, src, err := l.ReadFromUDP(b)
		if err != nil {
			log.Fatal("ReadFromUDP failed:", err)
		}
		h(src, n, b)
	}
}
