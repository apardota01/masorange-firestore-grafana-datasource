# Release v1.0.0 - Advanced Firestore Datasource

## 🚀 Features

### NEW FEATURES
- ✨ Advanced GROUP BY support with all aggregation functions (COUNT, SUM, AVG, MIN, MAX)
- ✨ ORDER BY support for both regular fields and aggregate functions  
- ✨ Nested field querying (clientData.BrandCliente syntax)
- ✨ Grafana global variables support ($__from, $__to)
- ✨ Complex WHERE clause parsing with manual filtering
- ✨ Smart query routing between FireQL and native SDK

### IMPROVEMENTS
- 🚀 Removed automatic LIMIT constraints - full user control
- 🚀 Enhanced SQL parsing with newline and formatting support
- 🚀 Cross-platform binary support (Linux, macOS, Windows - AMD64/ARM64)
- 🚀 Robust error handling for empty results and edge cases

### TECHNICAL
- ⚡ Automatic query routing for optimal performance
- ⚡ Manual filtering to bypass Firestore index requirements
- ⚡ Proper data type handling in all scenarios

## 📦 Downloads

- **Plugin Archive**: masorange-firestore-datasource-v1.0.0.zip
- **Checksums**: See assets below

## 🔐 Checksums

- **SHA1**: 9ac8182864af9740b6c5c82fb6885902a2ce2ac0
- **MD5**: e1080cfdaffe09b39c3cb58db39a966f
