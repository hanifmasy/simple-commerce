# Simple E-commerce API Endpoint Using Golang


## Prerequisites

- Go installed on your machine
- PostgreSQL installed and running
- SMTP server for sending emails
- Environment variables set in a `.env` file (Refer to `.env.example`)

## Setup

1. Clone the repository:

   ```bash
   git clone https://github.com/hanifmasy/simple-commerce.git
   cd simple-commerce
   ```

2. Install dependencies:

   ```bash
   go get -u ./...
   ```

3. Create a PostgreSQL database and set up tables:

   ```bash
   createdb your_database_name
   ```

   Update the `.env` file with your database credentials:

   ```bash
   DB_USERNAME=your_db_username
   DB_PASSWORD=your_db_password
   DB_NAME=your_database_name
   ```

   Run the database initialization script:

   ```bash
   go run main.go
   ```

4. Configure SMTP settings:

   Update the `.env` file with your SMTP server credentials:

   ```bash
   SMTP_SERVER=smtp.example.com
   SMTP_PORT=587
   SMTP_USERNAME=your_smtp_username
   SMTP_PASSWORD=your_smtp_password
   ```


## Running the Application

Run the following command to start the application:

```bash
go run main.go
```

The application will be accessible at [http://localhost:your_port](http://localhost:your_port), example `http://locahost:8080`.

## API Endpoints

- **Place Order:**
  - Endpoint: `/place-order`
  - Method: POST

- **Customer View Orders:**
  - Endpoint: `/customer/orders`
  - Method: GET

- **Admin View All Orders:**
  - Endpoint: `/admin/orders`
  - Method: GET

## Background Task

The application includes a background task that sends email reminders for pending orders.

## Notes

- Make sure to replace placeholder values (your_*) with your actual configuration.
