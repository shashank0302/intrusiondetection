#!/usr/bin/env python3
"""
AI-based Zero-Day Detector
Subscribes to Redis Pub/Sub channel and uses IsolationForest for anomaly detection.
"""

import redis
import numpy as np
from sklearn.ensemble import IsolationForest
from collections import deque
from datetime import datetime

# Configuration
REDIS_HOST = "localhost"
REDIS_PORT = 6379
CHANNEL = "traffic_monitor"
ALERT_CHANNEL = "ai_alerts"  # Channel to publish alerts to dashboard
BUFFER_SIZE = 1000
RETRAIN_INTERVAL = 100
CONTAMINATION = 0.01  # Expected anomaly rate

def main():
    print("╔══════════════════════════════════════════╗")
    print("║     AI ZERO-DAY DETECTOR                 ║")
    print("╠══════════════════════════════════════════╣")
    print(f"║ Redis:          {REDIS_HOST}:{REDIS_PORT}              ║")
    print(f"║ Channel:        {CHANNEL}          ║")
    print(f"║ Buffer Size:    {BUFFER_SIZE}                       ║")
    print(f"║ Retrain Every:  {RETRAIN_INTERVAL} requests              ║")
    print(f"║ Contamination:  {CONTAMINATION}                       ║")
    print("╚══════════════════════════════════════════╝")
    print()

    # Connect to Redis
    r = redis.Redis(host=REDIS_HOST, port=REDIS_PORT, decode_responses=True)
    
    try:
        r.ping()
        print(f"[✓] Connected to Redis at {REDIS_HOST}:{REDIS_PORT}")
    except redis.ConnectionError as e:
        print(f"[✗] Failed to connect to Redis: {e}")
        return

    # Subscribe to channel
    pubsub = r.pubsub()
    pubsub.subscribe(CHANNEL)
    print(f"[✓] Subscribed to channel: {CHANNEL}")
    print()
    print("Listening for traffic... (Ctrl+C to stop)")
    print("-" * 50)

    # Buffer for storing payload sizes (feature for ML)
    buffer = deque(maxlen=BUFFER_SIZE)
    
    # IP tracking for alerts
    ip_buffer = deque(maxlen=BUFFER_SIZE)
    
    # Initialize model (will be trained after enough data)
    model = None
    request_count = 0
    anomaly_count = 0

    for message in pubsub.listen():
        if message["type"] != "message":
            continue

        try:
            # Parse message: ip|timestamp|payload_size
            data = message["data"]
            parts = data.split("|")
            if len(parts) != 3:
                continue

            ip, timestamp, payload_size = parts[0], int(parts[1]), int(parts[2])
            
            # Add to buffer
            buffer.append(payload_size)
            ip_buffer.append(ip)
            request_count += 1

            # Need minimum data to train
            if len(buffer) < 100:
                if request_count % 50 == 0:
                    print(f"[INFO] Collecting baseline data... {len(buffer)}/100")
                continue

            # Retrain model every RETRAIN_INTERVAL requests
            if request_count % RETRAIN_INTERVAL == 0:
                X = np.array(list(buffer)).reshape(-1, 1)
                model = IsolationForest(
                    contamination=CONTAMINATION,
                    random_state=42,
                    n_estimators=100
                )
                model.fit(X)
                print(f"[TRAIN] Model retrained on {len(buffer)} samples (total processed: {request_count})")

            # Skip prediction if model not ready
            if model is None:
                continue

            # Predict current request
            X_current = np.array([[payload_size]])
            prediction = model.predict(X_current)[0]

            # -1 indicates anomaly
            if prediction == -1:
                anomaly_count += 1
                timestamp_str = datetime.now().strftime("%H:%M:%S")
                print(f"[{timestamp_str}] 🚨 AI DETECTED ZERO-DAY ATTACK from IP: {ip} (payload_size={payload_size})")
                
                # Publish alert to Redis for dashboard
                import json
                alert_payload = json.dumps({
                    "ip": ip,
                    "payload_size": payload_size,
                    "timestamp": int(datetime.now().timestamp()),
                    "type": "zero_day"
                })
                r.publish(ALERT_CHANNEL, alert_payload)

            # Periodic stats
            if request_count % 500 == 0:
                anomaly_rate = (anomaly_count / request_count) * 100 if request_count > 0 else 0
                print(f"[STATS] Processed: {request_count} | Anomalies: {anomaly_count} ({anomaly_rate:.2f}%)")

        except Exception as e:
            print(f"[ERROR] {e}")
            continue


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        print("\n\nShutting down AI worker...")
