package main

import (
	"database/sql"
  "encoding/csv"
  "encoding/json"
  "io/ioutil"
	"fmt"
	"log"
	"net/http"
  "net/smtp"
  "os"
  "strconv"
	"time"

  "github.com/golang/time/rate"
	"github.com/gorilla/mux"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

var db *sql.DB

// Advise: This token only as examples, change for better encryption
var customerToken = "customer_token"
var adminToken = "admin_token"

// Limiter for Request per minute
var rateLimiter = NewRateLimiter(100, time.Minute)

// Rate limiter SendEmailReminder to allow 1 task per day
var taskLimiter = NewRateLimiter(1, 24*time.Hour)

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}

	initDB()

	r := mux.NewRouter()
	r.HandleFunc("/place-order", RateLimitMiddleware(AuthMiddleware(PlaceOrderHandler, "customer"))).Methods("POST")
  r.HandleFunc("/customer/orders", AuthMiddleware(CustomerOrdersHandler, "customer")).Methods("GET")
	r.HandleFunc("/admin/orders", RateLimitMiddleware(AuthMiddleware(AdminOrdersHandler, "admin"))).Methods("GET")

	go BackgroundTask()

  http.Handle("/", r)
	serverPort := os.Getenv("SERVER_PORT")
	log.Fatal(http.ListenAndServe(":"+serverPort, nil))
}

// Configure SMTP settings using environment variables
var smtpConfig struct {
	SMTPServer   string `env:"SMTP_SERVER"`
	SMTPPort     int    `env:"SMTP_PORT"`
	SMTPUsername string `env:"SMTP_USERNAME"`
	SMTPPassword string `env:"SMTP_PASSWORD"`
}

func init() {
	err := envconfig.Process("", &smtpConfig)
	if err != nil {
		log.Fatalf("Error processing SMTP environment variables: %v", err)
	}
}

func initDB() {
  // Use envConfig struct to load environment variables
	var envConfig struct {
		DBUsername string `env:"DB_USERNAME"`
		DBPassword string `env:"DB_PASSWORD"`
		DBName     string `env:"DB_NAME"`
	}

	connectionString := fmt.Sprintf("user=%s password=%s dbname=%s sslmode=disable", envConfig.DBUsername, envConfig.DBPassword, envConfig.DBName)

	var err error
	db, err = sql.Open("postgres", connectionString)
	if err != nil {
		log.Fatal(err)
	}

	err = db.Ping()
	if err != nil {
		log.Fatal(err)
	}

	// Initialize database schema (create tables)
	createTableSQL := `
		CREATE TABLE IF NOT EXISTS products (
			id SERIAL PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			price DECIMAL NOT NULL,
			description TEXT,
			image_url VARCHAR(255)
		);

		CREATE TABLE IF NOT EXISTS customers (
			id SERIAL PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			email VARCHAR(255) NOT NULL,
			password VARCHAR(255) NOT NULL
		);

		CREATE TABLE IF NOT EXISTS orders (
			id SERIAL PRIMARY KEY,
			customer_id INT NOT NULL,
			date TIMESTAMP NOT NULL,
			status VARCHAR(50) NOT NULL,
			FOREIGN KEY (customer_id) REFERENCES customers(id)
		);

		CREATE TABLE IF NOT EXISTS order_products (
			order_id INT NOT NULL,
			product_id INT NOT NULL,
			PRIMARY KEY (order_id, product_id),
			FOREIGN KEY (order_id) REFERENCES orders(id),
			FOREIGN KEY (product_id) REFERENCES products(id)
		);
	`

	_, err = db.Exec(createTableSQL)
	if err != nil {
		log.Fatal(err)
	}
}



// CUSTOMER PLACE AN ORDER
func PlaceOrderHandler(w http.ResponseWriter, r *http.Request) {
	// Validate input data
	var orderRequest OrderRequest
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Println("Error reading request body:", err)
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("Bad Request"))
		return
	}

	err = json.Unmarshal(body, &orderRequest)
	if err != nil {
		log.Println("Error decoding JSON:", err)
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("Invalid JSON format"))
		return
	}

	if err := validateOrderRequest(orderRequest); err != nil {
		log.Println("Validation error:", err)
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("Validation error: " + err.Error()))
		return
	}

	// Create a new order in the database
	orderID, err := createOrder(orderRequest)
	if err != nil {
		log.Println("Error creating order:", err)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal Server Error"))
		return
	}

	// Associate the ordered products with the order
	err = associateProducts(orderID, orderRequest.Products)
	if err != nil {
		log.Println("Error associating products with order:", err)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal Server Error"))
		return
	}

	// Generate CSV report
	err = GenerateCSVReport(orderID, orderRequest.CustomerID)
	if err != nil {
		log.Println("Error generating CSV report:", err)
	}

	w.WriteHeader(http.StatusCreated)
	w.Write([]byte("Order placed successfully"))
}

func GenerateCSVReport(orderID, customerID int) error {
  // Query order details for the CSV report
	order, err := getOrderDetails(orderID, customerID)
	if err != nil {
		return err
	}

	// Open a new CSV file for writing
	file, err := os.Create("order_report.csv")
	if err != nil {
		return err
	}
	defer file.Close()

	// Create a CSV writer
	writer := csv.NewWriter(file)
	defer writer.Flush()

	// Write header
	header := []string{"Order ID", "Customer ID", "Date", "Status", "Product ID", "Product Name", "Price", "Quantity"}
	if err := writer.Write(header); err != nil {
		return err
	}

	// Write order details
	for _, product := range order.Products {
		row := []string{
			strconv.Itoa(order.ID),
			strconv.Itoa(order.CustomerID),
			order.Date.Format("2006-01-02 15:04:05"),
			order.Status,
			strconv.Itoa(product.ID),
			product.Name,
			strconv.FormatFloat(product.Price, 'f', 2, 64),
			strconv.Itoa(product.Quantity),
		}
		if err := writer.Write(row); err != nil {
			return err
		}
	}

	return nil
}

func getOrderDetails(orderID, customerID int) (*OrderWithProducts, error) {
  // Query order details with products
	rows, err := db.Query(`
		SELECT o.id as order_id, o.customer_id, o.date, o.status,
			   p.id as product_id, p.name as product_name, p.price, op.quantity
		FROM orders o
		JOIN order_products op ON o.id = op.order_id
		JOIN products p ON op.product_id = p.id
		WHERE o.id = $1 AND o.customer_id = $2
	`, orderID, customerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	order := &OrderWithProducts{
		ID:         orderID,
		CustomerID: customerID,
		Products:   make([]Product, 0),
	}

	for rows.Next() {
		var product Product
		if err := rows.Scan(&order.ID, &order.CustomerID, &order.Date, &order.Status,
			&product.ID, &product.Name, &product.Price, &product.Quantity); err != nil {
			return nil, err
		}
		order.Products = append(order.Products, product)
	}

	return order, nil
}



// CUSTOMER VIEW ORDERS
func CustomerOrdersHandler(w http.ResponseWriter, r *http.Request) {
    // Retrieve customer orders with product details
  	customerID := getCustomerID(r)
  	orders, err := getCustomerOrdersWithProducts(customerID)
  	if err != nil {
  		log.Println("Error retrieving customer orders:", err)
  		w.WriteHeader(http.StatusInternalServerError)
  		w.Write([]byte("Internal Server Error"))
  		return
  	}

  	// Convert orders to JSON
  	response, err := json.Marshal(orders)
  	if err != nil {
  		log.Println("Error encoding customer orders to JSON:", err)
  		w.WriteHeader(http.StatusInternalServerError)
  		w.Write([]byte("Internal Server Error"))
  		return
  	}

  	// Respond with the list of customer orders
  	w.Header().Set("Content-Type", "application/json")
  	w.WriteHeader(http.StatusOK)
  	w.Write(response)
}

func getCustomerID(r *http.Request) int {
  // Assume a custom header 'X-Customer-ID' in the request.
	customerIDHeader := r.Header.Get("X-Customer-ID")

	// Parse the customer ID from the header
	customerID, err := strconv.Atoi(customerIDHeader)
	if err != nil {
		// Handle the error or return a default value
		return 0
	}

	return customerID
}

func getCustomerOrdersWithProducts(customerID int) ([]OrderWithProducts, error) {
  // Query customer orders with product details
	rows, err := db.Query(`
		SELECT o.id as order_id, o.date, o.status,
			   p.id as product_id, p.name as product_name, p.price, p.description, p.image_url
		FROM orders o
		JOIN order_products op ON o.id = op.order_id
		JOIN products p ON op.product_id = p.id
		WHERE o.customer_id = $1
	`, customerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	orders := make(map[int]*OrderWithProducts)
	for rows.Next() {
		var orderID int
		var orderDate time.Time
		var orderStatus, productName, productDescription, imageURL string
		var productID int
		var productPrice float64

		if err := rows.Scan(&orderID, &orderDate, &orderStatus,
			&productID, &productName, &productPrice, &productDescription, &imageURL); err != nil {
			return nil, err
		}

		if order, ok := orders[orderID]; ok {
			// Order already exists, add product to it
			order.Products = append(order.Products, Product{
				ID:          productID,
				Name:        productName,
				Price:       productPrice,
				Description: productDescription,
				ImageURL:    imageURL,
			})
		} else {
			// Create a new order and add the product
			orders[orderID] = &OrderWithProducts{
				ID:      orderID,
				Date:    orderDate,
				Status:  orderStatus,
				Products: []Product{{
					ID:          productID,
					Name:        productName,
					Price:       productPrice,
					Description: productDescription,
					ImageURL:    imageURL,
				}},
			}
		}
	}

	// Convert map to slice
	var result []OrderWithProducts
	for _, order := range orders {
		result = append(result, *order)
	}

	return result, nil
}



// ADMIN VIEW ALL ORDERS
func AdminOrdersHandler(w http.ResponseWriter, r *http.Request) {
  // Retrieve all orders with product details
  	orders, err := getAllOrdersWithProducts()
  	if err != nil {
  		log.Println("Error retrieving orders:", err)
  		w.WriteHeader(http.StatusInternalServerError)
  		w.Write([]byte("Internal Server Error"))
  		return
  	}

  	// Convert orders to JSON
  	response, err := json.Marshal(orders)
  	if err != nil {
  		log.Println("Error encoding orders to JSON:", err)
  		w.WriteHeader(http.StatusInternalServerError)
  		w.Write([]byte("Internal Server Error"))
  		return
  	}

  	// Respond with the list of orders
  	w.Header().Set("Content-Type", "application/json")
  	w.WriteHeader(http.StatusOK)
  	w.Write(response)
}

func getAllOrdersWithProducts() ([]OrderWithProducts, error) {
	// Query all orders with product details
	rows, err := db.Query(`
		SELECT o.id as order_id, o.customer_id, o.date, o.status,
			   p.id as product_id, p.name as product_name, p.price, p.description, p.image_url
		FROM orders o
		JOIN order_products op ON o.id = op.order_id
		JOIN products p ON op.product_id = p.id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	orders := make(map[int]*OrderWithProducts)
	for rows.Next() {
		var orderID, customerID int
		var orderDate time.Time
		var orderStatus, productName, productDescription, imageURL string
		var productID int
		var productPrice float64

		if err := rows.Scan(&orderID, &customerID, &orderDate, &orderStatus,
			&productID, &productName, &productPrice, &productDescription, &imageURL); err != nil {
			return nil, err
		}

		if order, ok := orders[orderID]; ok {
			// Order already exists, add product to it
			order.Products = append(order.Products, Product{
				ID:          productID,
				Name:        productName,
				Price:       productPrice,
				Description: productDescription,
				ImageURL:    imageURL,
			})
		} else {
			// Create a new order and add the product
			orders[orderID] = &OrderWithProducts{
				ID:        orderID,
				CustomerID: customerID,
				Date:      orderDate,
				Status:    orderStatus,
				Products: []Product{{
					ID:          productID,
					Name:        productName,
					Price:       productPrice,
					Description: productDescription,
					ImageURL:    imageURL,
				}},
			}
		}
	}

	// Convert map to slice
	var result []OrderWithProducts
	for _, order := range orders {
		result = append(result, *order)
	}

	return result, nil
}

type OrderWithProducts struct {
	ID         int       `json:"order_id"`
	CustomerID int       `json:"customer_id"`
	Date       time.Time `json:"date"`
	Status     string    `json:"status"`
	Products   []Product `json:"products"`
}

type Product struct {
	ID          int     `json:"product_id"`
	Name        string  `json:"product_name"`
	Price       float64 `json:"price"`
	Description string  `json:"description"`
	ImageURL    string  `json:"image_url"`
}



// BACKGROUND TASK
func BackgroundTask() {
	for {
		if taskLimiter.Allow("background-task") {
			SendPendingOrderReminders()

			// Sleep for the remaining time until the next day
			now := time.Now()
			nextMidnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
			timeUntilMidnight := nextMidnight.Sub(now)
			time.Sleep(timeUntilMidnight)
		} else {
			// If the rate limit is exceeded, sleep for a shorter time before trying again
			time.Sleep(1 * time.Hour)
		}
	}
}

func SendPendingOrderReminders() {
	rows, err := db.Query("SELECT id, customer_email FROM orders WHERE status = 'Pending'")
	if err != nil {
		log.Println("Error querying pending orders:", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var orderID int
		var customerEmail string

		if err := rows.Scan(&orderID, &customerEmail); err != nil {
			log.Println("Error scanning row:", err)
			continue
		}

		// Send email using SMTP
		SendEmailReminder(customerEmail, orderID)
	}
}

func SendEmailReminder(to string, orderID int) {
	subject := "Pending Order Reminder"
	body := fmt.Sprintf("Dear customer, your order (ID: %d) is pending. Please complete your checkout process.", orderID)

	message := fmt.Sprintf("To: %s\r\nSubject: %s\r\n\r\n%s", to, subject, body)

	auth := smtp.PlainAuth("", smtpConfig.SMTPUsername, smtpConfig.SMTPPassword, smtpConfig.SMTPServer)
	err := smtp.SendMail(fmt.Sprintf("%s:%d", smtpConfig.SMTPServer, smtpConfig.SMTPPort), auth, smtpConfig.SMTPUsername, []string{to}, []byte(message))
	if err != nil {
		log.Printf("Error sending email to %s for order %d: %v", to, orderID, err)
	}
}



// AUTH & LIMITER
func AuthMiddleware(next http.HandlerFunc, role string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("Authorization")

		switch role {
		case "customer":
			if token != customerToken {
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte("Unauthorized"))
				return
			}
		case "admin":
			if token != adminToken {
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte("Unauthorized"))
				return
			}
		default:
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("Internal Server Error"))
			return
		}

		next.ServeHTTP(w, r)
	}
}

// Implement API rate limiter middleware
func RateLimitMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !rateLimiter.Allow(r.RemoteAddr) {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte("Rate limit exceeded"))
			return
		}

		next.ServeHTTP(w, r)
	}
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(limit int, window time.Duration) *rate.Limiter {
	return rate.NewLimiter(rate.Limit(limit), int(window.Seconds()))
}
