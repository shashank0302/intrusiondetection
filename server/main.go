package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
	pb "github.com/shashank/intrusiondetection/proto"
	"google.golang.org/grpc"
)

const (
	grpcPort         = ":50051"
	httpPort         = ":8080"
	secretKey        = "my-super-secret-key"
	redisAddr        = "localhost:6379"
	rateLimit        = 100               // max requests
	rateLimitWindow  = 10 * time.Second  // per window
	trafficMonitorCh = "traffic_monitor" // Redis Pub/Sub channel for AI worker
	aiAlertsCh       = "ai_alerts"       // Redis Pub/Sub channel for AI alerts
	localBlockTTL    = 60 * time.Second  // L1 cache TTL for blocked IPs
)

var rdb *redis.Client

// ============== Stats Tracking ==============

// Stats tracks request metrics atomically
type Stats struct {
	requestsThisSecond atomic.Int64
	blockedThisSecond  atomic.Int64
	totalRequests      atomic.Int64
	totalBlocked       atomic.Int64
}

var stats = &Stats{}

// DashboardPayload is sent to WebSocket clients
type DashboardPayload struct {
	RPS       int64 `json:"rps"`
	Blocked   int64 `json:"blocked"`
	Timestamp int64 `json:"timestamp"`
}

// ============== WebSocket Hub ==============

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for development
	},
}

// WebSocketHub manages all WebSocket connections
type WebSocketHub struct {
	mu      sync.RWMutex
	clients map[*websocket.Conn]bool
}

var wsHub = &WebSocketHub{
	clients: make(map[*websocket.Conn]bool),
}

func (h *WebSocketHub) Add(conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[conn] = true
	log.Printf("WebSocket client connected. Total: %d", len(h.clients))
}

func (h *WebSocketHub) Remove(conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, conn)
	conn.Close()
	log.Printf("WebSocket client disconnected. Total: %d", len(h.clients))
}

func (h *WebSocketHub) Broadcast(payload DashboardPayload) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	data, err := json.Marshal(payload)
	if err != nil {
		return
	}

	for conn := range h.clients {
		err := conn.WriteMessage(websocket.TextMessage, data)
		if err != nil {
			go h.Remove(conn)
		}
	}
}

// BroadcastRaw sends raw JSON data to all clients
func (h *WebSocketHub) BroadcastRaw(data []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for conn := range h.clients {
		err := conn.WriteMessage(websocket.TextMessage, data)
		if err != nil {
			go h.Remove(conn)
		}
	}
}

// AIAlertPayload wraps AI worker alerts for dashboard
type AIAlertPayload struct {
	Type        string `json:"type"`
	IP          string `json:"ip"`
	PayloadSize int    `json:"payload_size"`
	Timestamp   int64  `json:"timestamp"`
}

// startAIAlertSubscriber listens for AI worker alerts and forwards to WebSocket
func startAIAlertSubscriber(ctx context.Context) {
	pubsub := rdb.Subscribe(ctx, aiAlertsCh)
	defer pubsub.Close()

	log.Printf("Subscribed to AI alerts channel: %s", aiAlertsCh)

	ch := pubsub.Channel()
	for msg := range ch {
		// Parse and re-wrap with explicit type for dashboard
		var alert AIAlertPayload
		if err := json.Unmarshal([]byte(msg.Payload), &alert); err != nil {
			log.Printf("AI alert parse error: %v", err)
			continue
		}
		alert.Type = "ai_alert"

		data, err := json.Marshal(alert)
		if err != nil {
			continue
		}

		wsHub.BroadcastRaw(data)
		log.Printf("AI Alert forwarded: IP=%s, PayloadSize=%d", alert.IP, alert.PayloadSize)
	}
}

// wsHandler handles WebSocket upgrade requests
func wsHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}

	wsHub.Add(conn)

	// Keep connection alive, remove on error
	go func() {
		defer wsHub.Remove(conn)
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
		}
	}()
}

// startStatsBroadcaster sends stats to all WebSocket clients every second
func startStatsBroadcaster() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		// Get and reset per-second counters
		rps := stats.requestsThisSecond.Swap(0)
		blocked := stats.blockedThisSecond.Swap(0)

		payload := DashboardPayload{
			RPS:       rps,
			Blocked:   blocked,
			Timestamp: time.Now().Unix(),
		}

		wsHub.Broadcast(payload)
	}
}

// ============== LocalBlocklist (L1 Cache) ==============

type LocalBlocklist struct {
	mu    sync.RWMutex
	items map[string]time.Time
}

var localBlocklist = &LocalBlocklist{
	items: make(map[string]time.Time),
}

func (b *LocalBlocklist) IsBlocked(ip string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()

	expiry, exists := b.items[ip]
	if !exists {
		return false
	}

	if time.Now().Before(expiry) {
		return true
	}
	return false
}

func (b *LocalBlocklist) Block(ip string, ttl time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.items[ip] = time.Now().Add(ttl)
}

func (b *LocalBlocklist) Cleanup() {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	for ip, expiry := range b.items {
		if now.After(expiry) {
			delete(b.items, ip)
		}
	}
}

// ============== Rate Limiting ==============

var slidingWindowScript = redis.NewScript(`
	local key = KEYS[1]
	local now = tonumber(ARGV[1])
	local window = tonumber(ARGV[2])
	local limit = tonumber(ARGV[3])
	local clearBefore = now - window

	redis.call('ZREMRANGEBYSCORE', key, '-inf', clearBefore)
	local count = redis.call('ZCARD', key)

	if count < limit then
		redis.call('ZADD', key, now, now .. '-' .. math.random(1000000))
		redis.call('PEXPIRE', key, window)
		return 1
	else
		return 0
	end
`)

// ============== gRPC Server ==============

type Server struct {
	pb.UnimplementedIntrusionDetectionServiceServer
}

func verifySignature(payload []byte, timestamp int64, signature string, secretKey string) bool {
	mac := hmac.New(sha256.New, []byte(secretKey))
	mac.Write(payload)

	tsBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(tsBytes, uint64(timestamp))
	mac.Write(tsBytes)

	expectedSig := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expectedSig), []byte(signature))
}

func checkRateLimit(ctx context.Context, ip string) bool {
	if localBlocklist.IsBlocked(ip) {
		return false
	}

	key := fmt.Sprintf("ratelimit:%s", ip)
	now := time.Now().UnixMilli()
	windowMs := rateLimitWindow.Milliseconds()

	result, err := slidingWindowScript.Run(ctx, rdb, []string{key}, now, windowMs, rateLimit).Int()
	if err != nil {
		log.Printf("Redis error: %v (allowing request)", err)
		return true
	}

	if result == 0 {
		localBlocklist.Block(ip, localBlockTTL)
		return false
	}

	return true
}

func publishToAIWorker(ip string, timestamp int64, payloadSize int) {
	msg := fmt.Sprintf("%s|%d|%d", ip, timestamp, payloadSize)
	go rdb.Publish(context.Background(), trafficMonitorCh, msg)
}

func (s *Server) StreamLogs(stream pb.IntrusionDetectionService_StreamLogsServer) error {
	log.Println("Client connected to StreamLogs")
	ctx := stream.Context()

	for {
		req, err := stream.Recv()
		if err == io.EOF {
			log.Println("Client closed stream")
			return nil
		}
		if err != nil {
			log.Printf("Receive error: %v", err)
			return err
		}

		// Track request
		stats.requestsThisSecond.Add(1)
		stats.totalRequests.Add(1)

		ip := req.GetIpAddress()
		var resp *pb.LogResponse
		blocked := false

		if !verifySignature(req.GetPayload(), req.GetTimestamp(), req.GetSignature(), secretKey) {
			resp = &pb.LogResponse{
				Status:  "BLOCKED_INVALID_SIG",
				Message: "Invalid HMAC signature",
			}
			blocked = true
		} else if !checkRateLimit(ctx, ip) {
			resp = &pb.LogResponse{
				Status:  "BLOCKED_RATE_LIMIT",
				Message: fmt.Sprintf("Rate limit exceeded: %d requests per %v", rateLimit, rateLimitWindow),
			}
			blocked = true
		} else {
			resp = &pb.LogResponse{
				Status:  "ALLOWED",
				Message: "Request processed successfully",
			}
		}

		// Track blocks
		if blocked {
			stats.blockedThisSecond.Add(1)
			stats.totalBlocked.Add(1)
		}

		if err := stream.Send(resp); err != nil {
			log.Printf("Send error: %v", err)
			return err
		}

		publishToAIWorker(ip, req.GetTimestamp(), len(req.GetPayload()))
	}
}

func main() {
	// Initialize Redis
	rdb = redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})

	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}
	log.Printf("Connected to Redis at %s", redisAddr)

	// Start L1 cache cleanup
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			localBlocklist.Cleanup()
		}
	}()

	// Start WebSocket stats broadcaster
	go startStatsBroadcaster()

	// Start AI alerts subscriber (forwards AI worker alerts to dashboard)
	go startAIAlertSubscriber(ctx)

	// Start HTTP server for WebSocket
	go func() {
		http.HandleFunc("/ws", wsHandler)
		log.Printf("WebSocket server listening on %s", httpPort)
		if err := http.ListenAndServe(httpPort, nil); err != nil {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	// Start gRPC server
	lis, err := net.Listen("tcp", grpcPort)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	pb.RegisterIntrusionDetectionServiceServer(grpcServer, &Server{})

	log.Printf("gRPC server listening on %s", grpcPort)
	log.Printf("Rate limit: %d requests per %v per IP", rateLimit, rateLimitWindow)

	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
