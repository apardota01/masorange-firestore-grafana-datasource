# Advanced Firestore Datasource for Grafana

[Google Firestore](https://cloud.google.com/firestore) Data Source Plugin for [Grafana](https://grafana.com/) with advanced SQL-like query capabilities.

This enhanced Firestore datasource plugin enables seamless integration of Firestore data into Grafana dashboards with powerful SQL-like querying, aggregation functions, and nested field support.

The plugin combines [FireQL](https://github.com/pgollangi/FireQL) for complex queries with native Firestore SDK for optimal performance, automatically routing queries to the best execution engine.

> **Key Enhancement**: Advanced query routing system that automatically selects between FireQL and native Firestore SDK based on query complexity for optimal performance.



![](assets/firestore-grafana-datasource.svg)

## Features

### 🚀 **Enhanced SQL Query Support**
- [x] **Advanced GROUP BY with Aggregations**: `COUNT(*)`, `SUM()`, `AVG()`, `MIN()`, `MAX()` functions
- [x] **ORDER BY Support**: Sort results by any field or aggregate function (ASC/DESC)
- [x] **Nested Field Queries**: Access nested document fields like `clientData.BrandCliente`
- [x] **Grafana Global Variables**: Use `$__from` and `$__to` for time range filtering
- [x] **Complex WHERE Clauses**: Multiple conditions with `AND` operator support
- [x] **Manual Filtering**: Bypass Firestore index requirements for complex queries

### 📊 **Core Datasource Features**
- [x] Use Google Firestore as a data source for Grafana dashboards
- [x] Configure Firestore data source with GCP `Project Id` and [`Service Account`](https://cloud.google.com/firestore/docs/security/iam) for authentication
- [x] Store `Service Account` data source configuration in Grafana encrypted storage [Secure JSON Data](https://grafana.com/docs/grafana/latest/developers/plugins/create-a-grafana-plugin/extend-a-plugin/add-authentication-for-data-source-plugins/#encrypt-data-source-configuration)
- [x] Query Firestore [collections](https://firebase.google.com/docs/firestore/data-model#collections) and path to collections
- [x] Auto detect data types: `string`, `number`, `boolean`, `json`, `time.Time`
- [x] Query selected fields from the collection
- [x] LIMIT query results (no automatic limits imposed)
- [x] Query [Collection Groups](https://firebase.blog/posts/2019/06/understanding-collection-group-queries)

### ⚡ **Performance & Reliability**
- [x] **Smart Query Routing**: Automatically uses native SDK or FireQL based on query complexity
- [x] **Robust Error Handling**: Proper handling of empty results and edge cases
- [x] **Cross-Platform Binaries**: Support for Linux, Windows, and macOS (AMD64/ARM64)

### Firestore data source configuration

![](src/screenshots/firestore-datasource-configuration.png)

### Using datasource
![](src/screenshots/query-with-firestore-datasource.png)

## Query Examples

### Basic Queries
```sql
-- Select all fields from a collection
SELECT * FROM users

-- Select specific fields
SELECT name, email, status FROM users

-- Filter with WHERE clause
SELECT * FROM orders WHERE status == "completed"

-- Use LIMIT to control result size
SELECT * FROM products LIMIT 50
```

### Advanced Aggregation Queries
```sql
-- Count records by status
SELECT status, COUNT(*) as total
FROM orders
GROUP BY status
ORDER BY total DESC

-- Average order value by customer
SELECT customerId, AVG(total) as avg_order
FROM orders
GROUP BY customerId
ORDER BY avg_order DESC

-- Monthly sales summary
SELECT DATE_TRUNC(createdAt, 'month') as month, SUM(amount) as total_sales
FROM transactions
WHERE createdAt >= $__from AND createdAt <= $__to
GROUP BY month
ORDER BY month ASC
```

### Nested Field Queries
```sql
-- Query nested fields
SELECT openTS, idLP, clientData.BrandCliente
FROM dialogs
WHERE clientData.BrandCliente == "yoigo"

-- Complex nested filtering
SELECT * FROM users
WHERE address.city == "Madrid" AND preferences.notifications == true
```

### Time-based Queries with Grafana Variables
```sql
-- Use Grafana time range variables
SELECT * FROM events
WHERE timestamp >= $__from AND timestamp <= $__to

-- Combine time filtering with other conditions
SELECT status, COUNT(*) as count
FROM dialogs
WHERE status == "closed" AND openTS >= $__from AND openTS <= $__to
GROUP BY status
```

## Installation

### For End Users

1. **Download the plugin**:
   - Download the latest release from the releases page
   - Extract the plugin files to your Grafana plugins directory

2. **Configure Grafana**:
   - Add the plugin to your Grafana configuration
   - Restart Grafana

3. **Add Data Source**:
   - Go to Configuration > Data Sources
   - Click "Add data source" and select "Firestore Datasource"
   - Configure your GCP Project ID and Service Account credentials

### For Grafana Cloud Users

This plugin can be submitted to the Grafana Plugin Catalog for easy installation on Grafana Cloud instances.

## Development

### Frontend

1. Install dependencies

   ```bash
   yarn install
   ```

2. Build plugin in development mode or run in watch mode

   ```bash
   yarn dev

   # or

   yarn watch
   ```

3. Build plugin in production mode

   ```bash
   yarn build
   ```

4. Run the tests (using Jest)

   ```bash
   # Runs the tests and watches for changes
   yarn test
   
   # Exists after running all the tests
   yarn lint:ci
   ```

5. Spin up a Grafana instance and run the plugin inside it (using Docker)

   ```bash
   yarn server
   ```

6. Run the E2E tests (using Cypress)

   ```bash
   # Spin up a Grafana instance first that we tests against 
   yarn server
   
   # Start the tests
   yarn e2e
   ```

7. Run the linter

   ```bash
   yarn lint
   
   # or

   yarn lint:fix
   ```

### Backend

1. Update [Grafana plugin SDK for Go](https://grafana.com/docs/grafana/latest/developers/plugins/backend/grafana-plugin-sdk-for-go/) dependency to the latest minor version:

   ```bash
   go get -u github.com/grafana/grafana-plugin-sdk-go
   go mod tidy
   ```

2. Build backend plugin binaries for Linux, Windows and Darwin:

   ```bash
   mage -v
   ```

3. List all available Mage targets for additional commands:

   ```bash
   mage -l
   ```

#### Push a version tag

To trigger the workflow we need to push a version tag to github. This can be achieved with the following steps:

1. Run `npm version <major|minor|patch>`
2. Run `git push origin main --follow-tags`


## Technical Details

### Query Routing Logic
The plugin intelligently routes queries between two execution engines:

- **Native Firestore SDK**: For queries with Grafana variables (`$__from`, `$__to`) or GROUP BY clauses
- **FireQL Engine**: For simple queries without time variables or aggregations

### Supported Aggregation Functions
- `COUNT(*)` - Count all records in each group
- `SUM(field)` - Sum numeric values
- `AVG(field)` - Calculate average of numeric values
- `MIN(field)` - Find minimum value
- `MAX(field)` - Find maximum value

### Field Access Patterns
- **Simple fields**: `fieldName`
- **Nested fields**: `parentField.childField`
- **Deep nesting**: `level1.level2.level3`

### Supported Platforms
- Linux (AMD64, ARM64)
- macOS (AMD64, ARM64)
- Windows (AMD64)

## Changelog

### v1.0.0 (2025-09-22)
- ✨ **NEW**: Advanced GROUP BY support with all aggregation functions
- ✨ **NEW**: ORDER BY support for both regular fields and aggregate functions
- ✨ **NEW**: Nested field querying (`clientData.BrandCliente`)
- ✨ **NEW**: Grafana global variables support (`$__from`, `$__to`)
- ✨ **NEW**: Complex WHERE clause parsing with manual filtering
- ✨ **NEW**: Smart query routing between FireQL and native SDK
- 🐛 **FIX**: Panic handling for empty filtered results
- 🐛 **FIX**: Proper data type handling in empty data frames
- 🚀 **IMPROVEMENT**: Removed automatic LIMIT constraints
- 🚀 **IMPROVEMENT**: Enhanced SQL parsing with newline support
- 🚀 **IMPROVEMENT**: Cross-platform binary support

## Contributing
Thanks for considering contributing to this project!

Please read the [Contributions](CONTRIBUTING.md) and [Code of conduct](CODE_OF_CONDUCT.md).

Feel free to open an issue or submit a pull request!

## License

[MIT](LICENSE)
