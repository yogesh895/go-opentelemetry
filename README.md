# Go-opentelemetry App

## Steps to Run

1. **Clone the repository**:  
   ```bash
   git clone https://github.com/yogesh895/go-opentelemetry.git
   cd go-opentelemetry
   ```

2. **Install dependencies**:  
   ```bash
   go mod tidy
   ```

3. **Run the application**:  
   ```bash
   go run main.go
   ```

That’s it! 🎉


## API's to Test Metrics Data

**Add item**
```bash 
  curl -X POST -H "User-ID: user1" -H "Content-Type: application/json" \
  -d '{"id":"1","name":"Product 1","price":29.99,"quantity":2}' \
  http://localhost:8080/cart/add
```

**View cart**
```bash
curl -H "User-ID: user1" http://localhost:8080/cart/view
```

**Remove item**
```bash
curl -X POST -H "User-ID: user1" -H "Content-Type: application/json" \
  -d '{"id":"1"}' \
  http://localhost:8080/cart/remove
```

**View cart again to verify removal**
```bash
curl -H "User-ID: user1" http://localhost:8080/cart/view
```
