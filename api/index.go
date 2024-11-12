package handler

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

func Handler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	err := r.ParseForm()
	if err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	baseURL := r.FormValue("base")
	overlayStr := r.FormValue("overlay")

	opt, _ := redis.ParseURL(os.Getenv("REDIS_URL"))
	redisC := redis.NewClient(opt)

	if baseURL == "" || overlayStr == "" {
		http.Error(w, "Missing base URL or overlay image", http.StatusBadRequest)
		return
	}

	key := getRedisKey(baseURL, overlayStr)
	fmt.Println(key)
	cachedResp := redisC.Get(r.Context(), key)

	if cachedResp.Err() == nil && cachedResp.Val() != "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(cachedResp.Val()))
		fmt.Println("Cache hit!")
		return
	} else {
		fmt.Println(cachedResp.Err())
	}

	baseImg, err := fetchImage(baseURL)
	if err != nil {
		http.Error(w, "Failed to fetch base image: "+err.Error(), http.StatusBadRequest)
		return
	}

	overlayImg, err := base64ToPNG(overlayStr)
	if err != nil {
		http.Error(w, "Failed to decode overlay image: "+err.Error(), http.StatusBadRequest)
		return
	}

	bounds := baseImg.Bounds()
	composite := image.NewRGBA(bounds)

	draw.Draw(composite, bounds, baseImg, image.Point{}, draw.Over)

	draw.Draw(composite, bounds, overlayImg, image.Point{}, draw.Over)

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := composite.At(x, y).RGBA()
			r8 := uint8(r >> 8)
			g8 := uint8(g >> 8)
			b8 := uint8(b >> 8)

			if r8 == 0 && g8 == 0 && b8 == 254 {
				composite.Set(x, y, color.Transparent)
			}
		}
	}

	var buf bytes.Buffer
	err = png.Encode(&buf, composite)
	if err != nil {
		http.Error(w, "Failed to encode composite image", http.StatusInternalServerError)
		return
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	part, err := writer.CreateFormFile("file", "skin.png")
	if err != nil {
		http.Error(w, "Failed to create form file", http.StatusInternalServerError)
		return
	}
	_, err = io.Copy(part, bytes.NewReader(buf.Bytes()))
	if err != nil {
		http.Error(w, "Failed to copy image data", http.StatusInternalServerError)
		return
	}
	writer.Close()

	req, err := http.NewRequest("POST", "https://api.mineskin.org/generate/upload", body)
	if err != nil {
		http.Error(w, "Failed to create MineSkin request", http.StatusInternalServerError)
		return
	}

	req.Header.Set("Authorization", "Bearer "+os.Getenv("MINESKIN_API_KEY"))
	req.Header.Set("User-Agent", "Mineskin-Overlay/1.0 (Discord: per.ny)")
	req.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "Failed to send request to MineSkin: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "Failed to read MineSkin response", http.StatusInternalServerError)
		return
	}

	if resp.StatusCode != 200 {
		http.Error(w, "Failed to upload skin to MineSkin: "+string(respBody), http.StatusInternalServerError)
		return
	}

	set := redisC.Set(r.Context(), getRedisKey(baseURL, overlayStr), string(respBody), 0)
	if set.Err() != nil {
		http.Error(w, "Failed to set cache: "+set.Err().Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(respBody)
}

func getRedisKey(baseURL string, overlayStr string) string {
	return hashString(baseURL + ":" + overlayStr)
}

func hashString(input string) string {
	hasher := sha256.New()
	hasher.Write([]byte(input))
	hash := hasher.Sum(nil)

	encoded := base64.StdEncoding.EncodeToString(hash)

	return encoded[:16]
}

func fetchImage(url string) (image.Image, error) {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch image: %v", err)
	}
	defer resp.Body.Close()

	contentType := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "image/png") {
		return nil, fmt.Errorf("invalid content type: %s (expected image/png)", contentType)
	}

	img, err := png.Decode(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to decode PNG: %v", err)
	}

	return img, nil
}

func base64ToPNG(b64 string) (image.Image, error) {
	b64 = strings.TrimPrefix(b64, "data:image/png;base64,")

	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64: %v", err)
	}

	img, err := png.Decode(bytes.NewReader(decoded))
	if err != nil {
		return nil, fmt.Errorf("failed to decode PNG: %v", err)
	}

	return img, nil
}
