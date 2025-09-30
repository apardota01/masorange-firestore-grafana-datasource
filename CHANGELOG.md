# Changelog

## 1.0.21

### Features
- Advanced GROUP BY support with aggregation functions (COUNT, SUM, AVG, MIN, MAX)
- ORDER BY support for both regular fields and aggregate functions
- Nested field querying (e.g., `clientData.BrandCliente`)
- Grafana global variables support (`$__from`, `$__to`)
- Complex WHERE clause parsing with manual filtering
- Smart query routing between FireQL and native SDK

### Improvements
- Cross-platform binary support (Linux, macOS, Windows - AMD64/ARM64)
- Enhanced SQL parsing with newline support
- Removed automatic LIMIT constraints
- Proper error handling for empty results

### Bug Fixes
- Fixed panic handling for empty filtered results
- Proper data type handling in empty data frames
- Improved timestamp conversion logic
