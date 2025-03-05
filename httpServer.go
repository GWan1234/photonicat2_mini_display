package main

import (
	"image/png"
	"io/ioutil"
	"net/http"
	"strconv"
	"fmt"
	"log"
	"bytes"
	"image"
	"encoding/json"
)

var webFrame *image.RGBA

func serveFrame(w http.ResponseWriter, r *http.Request) {
	var err error
	var buf bytes.Buffer

	if webFrame == nil {
		webFrame = image.NewRGBA(image.Rect(0, 0, PCAT2_LCD_WIDTH, PCAT2_LCD_HEIGHT))
		drawRect(webFrame, 0, 0, PCAT2_LCD_WIDTH, PCAT2_LCD_HEIGHT, PCAT_BLACK)
	}

    frameMutex.RLock()
    //composite the frames
    err = copyImageToImageAt(webFrame, topBarFramebuffers[0], 0, 0)
    if err != nil {
        http.Error(w, "Failed to copy top bar frame", http.StatusInternalServerError)
        return
    }

    err = copyImageToImageAt(webFrame, middleFramebuffers[0], 0, PCAT2_TOP_BAR_HEIGHT)
    if err != nil {
        http.Error(w, "Failed to copy middle frame", http.StatusInternalServerError)
        return
    }   
    
    err = copyImageToImageAt(webFrame, footerFramebuffers[frames%2], 0, PCAT2_LCD_HEIGHT - PCAT2_FOOTER_HEIGHT)
    if err != nil {
        http.Error(w, "Failed to copy footer frame", http.StatusInternalServerError)
        return
    }
	frameMutex.RUnlock()

    if webFrame == nil {
        http.Error(w, "No frame available", http.StatusServiceUnavailable)
        return
    }

   
    err = png.Encode(&buf, webFrame)
    if err != nil {
        http.Error(w, "Failed to encode image", http.StatusInternalServerError)
        return
    }

    w.Header().Set("Content-Type", "image/png")
    w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
    buf.WriteTo(w)
}

func updateData(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
        return
    }

    body, err := ioutil.ReadAll(r.Body)
    if err != nil {
        http.Error(w, "Failed to read request body", http.StatusBadRequest)
        return
    }

    var data map[string]string
    err = json.Unmarshal(body, &data)
    if err != nil {
        http.Error(w, "Invalid JSON", http.StatusBadRequest)
        return
    }

    dataMutex.Lock()
    for k, v := range data {
        dynamicData[k] = v
    }
    dataMutex.Unlock()

    w.WriteHeader(http.StatusOK)
    fmt.Fprintln(w, "Data updated")
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "assets/html/index.html")
}

func httpServer() {
	http.HandleFunc("/", indexHandler)
	http.HandleFunc("/frame", serveFrame)
    http.HandleFunc("/data", updateData)
    log.Println("Starting HTTP server on :8080")
    log.Fatal(http.ListenAndServe(":8080", nil))
}