# Architecture

```
                  HTTP/3
                    │
            ┌───────▼─────────┐
            │   Directory     │  cmd/directory   HTTP/2 + TLS
            │  volume router  │  bbolt
            └───────┬─────────┘
                    │  302 Redirect
                    │
         ┌──────────▼──────────────┐
         │     store node          │  cmd/store      HTTP/1.1
         │  Store  (key address)   │  in-memory index
         │  Volume (offset address)│  needle block
         └─────────────────────────┘
```
