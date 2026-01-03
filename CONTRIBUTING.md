# Contributing to Dynamic Prefix Operator

Thank you for your interest in contributing to the Dynamic Prefix Operator!

## Development Setup

### Prerequisites

- Go 1.22 or later
- Docker or Podman
- kubectl
- Access to a Kubernetes cluster (kind, minikube, or remote)
- Optional: Cilium installed for integration testing

### Getting Started

1. Fork and clone the repository:
   ```bash
   git clone https://github.com/YOUR_USERNAME/dynamic-prefix-operator.git
   cd dynamic-prefix-operator
   ```

2. Install dependencies:
   ```bash
   make install-tools
   ```

3. Run tests:
   ```bash
   make test
   ```

4. Run locally against your cluster:
   ```bash
   make run
   ```

## Development Workflow

### Making Changes

1. Create a feature branch:
   ```bash
   git checkout -b feature/my-feature
   ```

2. Make your changes, following the code style guidelines

3. Add tests for new functionality

4. Run linting and tests:
   ```bash
   make lint
   make test
   ```

5. Commit with a descriptive message:
   ```bash
   git commit -m "feat: add support for XYZ"
   ```

6. Push and create a pull request

### Code Style

- Follow standard Go conventions
- Use `gofmt` for formatting (enforced by CI)
- Use meaningful variable and function names
- Add comments for exported functions and types
- Keep functions focused and small

### Commit Messages

We follow [Conventional Commits](https://www.conventionalcommits.org/):

- `feat:` - New feature
- `fix:` - Bug fix
- `docs:` - Documentation changes
- `test:` - Adding or updating tests
- `refactor:` - Code refactoring
- `chore:` - Maintenance tasks

### Testing

- **Unit tests**: Test individual functions in isolation
- **Integration tests**: Test with mock DHCPv6/NDP servers
- **E2E tests**: Test full operator functionality in a cluster

Run specific test suites:
```bash
make test                 # Unit tests
make test-integration     # Integration tests
make test-e2e            # End-to-end tests
```

## Adding a New Backend

To add support for a new CNI or IP pool type:

1. Create a new package under `internal/backend/`:
   ```go
   // internal/backend/mybackend/pool.go
   package mybackend

   import "github.com/jr42/dynamic-prefix-operator/internal/backend"

   type PoolBackend struct {
       client client.Client
   }

   func (b *PoolBackend) Name() string {
       return "mybackend-pool"
   }

   func (b *PoolBackend) Supports(target backend.TargetRef) bool {
       return target.Kind == "MyPoolKind"
   }

   func (b *PoolBackend) Update(ctx context.Context, target backend.TargetRef, prefix netip.Prefix, strategy backend.UpdateStrategy) error {
       // Implementation
   }
   ```

2. Register the backend in the manager

3. Add the new kind to the CRD enum

4. Add tests and documentation

## Reporting Issues

When reporting issues, please include:

- Operator version
- Kubernetes version
- Cilium version (if applicable)
- Steps to reproduce
- Expected vs actual behavior
- Relevant logs

## Questions?

- Open a [GitHub Discussion](https://github.com/jr42/dynamic-prefix-operator/discussions)
- Check existing issues and PRs

## License

By contributing, you agree that your contributions will be licensed under the Apache License 2.0.
