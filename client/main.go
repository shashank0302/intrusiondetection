package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	pb "github.com/shashank/intrusiondetection/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	serverAddr    = "localhost:50051"
	hmacSecretKey = "my-super-secret-key"
	numWorkers    = 50
	ddosIP        = "10.0.0.1" // Fixed IP for DDoS simulation
)

// Stats tracks response counts atomically
type Stats struct {
	sent        atomic.Int64
	allowed     atomic.Int64
	blockedSig  atomic.Int64
	blockedRate atomic.Int64
	errors      atomic.Int64
}

// generateSignature creates valid HMAC-SHA256
func generateSignature(payload []byte, timestamp int64) string {
	mac := hmac.New(sha256.New, []byte(hmacSecretKey))
	mac.Write(payload)

	tsBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(tsBytes, uint64(timestamp))
	mac.Write(tsBytes)

	return hex.EncodeToString(mac.Sum(nil))
}

// generatePayload creates random payload
func generatePayload() []byte {
	payload := make([]byte, 64+rand.Intn(128))
	rand.Read(payload)
	return payload
}

// randomIP generates a random IP
func randomIP() string {
	return fmt.Sprintf("192.168.%d.%d", rand.Intn(256), rand.Intn(256))
}

// worker simulates a botnet node
func worker(ctx context.Context, id int, stats *Stats, wg *sync.WaitGroup) {
	defer wg.Done()

	conn, err := grpc.Dial(serverAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Printf("[Worker %d] Connection failed: %v", id, err)
		return
	}
	defer conn.Close()

	client := pb.NewIntrusionDetectionServiceClient(conn)
	stream, err := client.StreamLogs(ctx)
	if err != nil {
		log.Printf("[Worker %d] Stream failed: %v", id, err)
		return
	}

	// Response receiver goroutine
	go func() {
		for {
			resp, err := stream.Recv()
			if err == io.EOF || ctx.Err() != nil {
				return
			}
			if err != nil {
				if ctx.Err() == nil {
					stats.errors.Add(1)
				}
				return
			}

			switch resp.GetStatus() {
			case "ALLOWED":
				stats.allowed.Add(1)
			case "BLOCKED_RATE_LIMIT":
				stats.blockedRate.Add(1)
			case "BLOCKED_INVALID_SIG":
				stats.blockedSig.Add(1)
			}
		}
	}()

	// Request sender - infinite loop
	for {
		select {
		case <-ctx.Done():
			stream.CloseSend()
			return
		default:
			payload := generatePayload()
			timestamp := time.Now().UnixNano()

			var ip, sig string
			roll := rand.Float64()

			switch {
			case roll < 0.90:
				// 90% - Valid signature, random IP (normal high traffic)
				ip = randomIP()
				sig = generateSignature(payload, timestamp)

			case roll < 0.95:
				// 5% - Invalid signature (tampering/hacking attempt)
				ip = randomIP()
				sig = "invalid-tampered-signature"

			default:
				// 5% - DDoS: spam from same IP to trigger rate limit
				ip = ddosIP
				sig = generateSignature(payload, timestamp)
			}

			req := &pb.LogRequest{
				IpAddress: ip,
				Payload:   payload,
				Timestamp: timestamp,
				Signature: sig,
			}

			if err := stream.Send(req); err != nil {
				if ctx.Err() == nil {
					stats.errors.Add(1)
				}
				return
			}
			stats.sent.Add(1)

			// Small delay to prevent CPU saturation
			time.Sleep(time.Millisecond)
		}
	}
}

func main() {
	rand.Seed(time.Now().UnixNano())

	fmt.Println("╔══════════════════════════════════════════╗")
	fmt.Println("║       DDoS ATTACK SIMULATOR              ║")
	fmt.Println("╠══════════════════════════════════════════╣")
	fmt.Printf("║ Server:       %s              ║\n", serverAddr)
	fmt.Printf("║ Workers:      %d (concurrent)            ║\n", numWorkers)
	fmt.Println("║ Attack Mix:                              ║")
	fmt.Println("║   90%% Valid traffic (random IPs)        ║")
	fmt.Println("║    5%% Invalid signatures (tampering)    ║")
	fmt.Println("║    5%% DDoS spam (fixed IP: 10.0.0.1)    ║")
	fmt.Println("╚══════════════════════════════════════════╝")
	fmt.Println("\nPress Ctrl+C to stop...\n")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\n\nShutting down...")
		cancel()
	}()

	var stats Stats
	var wg sync.WaitGroup

	// Spawn workers (botnet simulation)
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go worker(ctx, i, &stats, &wg)
	}

	// Stats printer - every second
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				fmt.Printf("Sent: %6d | Allowed: %6d | Blocked (Sig): %6d | Blocked (Rate): %6d | Errors: %d\n",
					stats.sent.Load(),
					stats.allowed.Load(),
					stats.blockedSig.Load(),
					stats.blockedRate.Load(),
					stats.errors.Load(),
				)
			}
		}
	}()

	wg.Wait()

	// Final stats
	fmt.Println("\n╔══════════════════════════════════════════╗")
	fmt.Println("║            FINAL RESULTS                 ║")
	fmt.Println("╠══════════════════════════════════════════╣")
	fmt.Printf("║ Total Sent:         %10d           ║\n", stats.sent.Load())
	fmt.Printf("║ Allowed:            %10d           ║\n", stats.allowed.Load())
	fmt.Printf("║ Blocked (Sig):      %10d           ║\n", stats.blockedSig.Load())
	fmt.Printf("║ Blocked (Rate):     %10d           ║\n", stats.blockedRate.Load())
	fmt.Printf("║ Errors:             %10d           ║\n", stats.errors.Load())
	fmt.Println("╚══════════════════════════════════════════╝")
}
