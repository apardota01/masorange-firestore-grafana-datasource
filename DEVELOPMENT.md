# Development Guide

This guide is for developers who want to contribute to the Firestore Grafana Datasource Plugin.

## Prerequisites

- Node.js 16+
- Go 1.21+
- Yarn package manager
- Docker (for local testing)

## Frontend Development

### 1. Install dependencies

```bash
yarn install
```

### 2. Build plugin in development mode or run in watch mode

```bash
yarn dev

# or

yarn watch
```

### 3. Build plugin in production mode

```bash
yarn build
```

### 4. Run the tests (using Jest)

```bash
# Runs the tests and watches for changes
yarn test

# Exists after running all the tests
yarn lint:ci
```

### 5. Spin up a Grafana instance and run the plugin inside it (using Docker)

```bash
yarn server
```

### 6. Run the E2E tests (using Cypress)

```bash
# Spin up a Grafana instance first that we tests against
yarn server

# Start the tests
yarn e2e
```

### 7. Run the linter

```bash
yarn lint

# or

yarn lint:fix
```

## Backend Development

### 1. Update Grafana plugin SDK for Go

Update [Grafana plugin SDK for Go](https://grafana.com/developers/plugin-tools/introduction/grafana-plugin-sdk-for-go) dependency to the latest minor version:

```bash
go get -u github.com/grafana/grafana-plugin-sdk-go
go mod tidy
```

### 2. Build backend plugin binaries

Build backend plugin binaries for Linux, Windows and Darwin:

```bash
mage -v
```

### 3. List all available Mage targets

List all available Mage targets for additional commands:

```bash
mage -l
```

## Release Process

### Push a version tag

To trigger the workflow we need to push a version tag to github. This can be achieved with the following steps:

1. Run `npm version <major|minor|patch>`
2. Run `git push origin main --follow-tags`

## Project Structure

```
firestore-grafana-datasource/
├── src/                    # Frontend TypeScript source
├── pkg/                    # Backend Go source
├── dist/                   # Built plugin files
├── .config/                # Build configuration
├── docker-compose.yml      # Local development setup
└── README.md              # User documentation
```

## Testing

### Unit Tests
```bash
yarn test
```

### E2E Tests
```bash
yarn e2e
```

### Go Tests
```bash
go test ./pkg/...
```

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests
5. Run the test suite
6. Submit a pull request

Please read our [Contributing Guidelines](CONTRIBUTING.md) and [Code of Conduct](CODE_OF_CONDUCT.md) before contributing.