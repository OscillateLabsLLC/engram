# Changelog

## [2.5.1](https://github.com/OscillateLabsLLC/engram/compare/v2.5.0...v2.5.1) (2026-04-23)


### Bug Fixes

* **deps:** bump buger/jsonparser to v1.1.2 to clear DoS alert ([dc8fb59](https://github.com/OscillateLabsLLC/engram/commit/dc8fb59f6308a4bd329f87a05b0c8c514ca14ede))
* **deps:** bump buger/jsonparser to v1.1.2 to clear DoS alert ([9be6eaa](https://github.com/OscillateLabsLLC/engram/commit/9be6eaa87c32dec8bbcfe0eef51e645969275b16))

## [2.5.0](https://github.com/OscillateLabsLLC/engram/compare/v2.4.0...v2.5.0) (2026-03-25)


### Features

* knowledge graph schema, entity resolution, and MCP tools ([2fd857b](https://github.com/OscillateLabsLLC/engram/commit/2fd857bf8623cb1eaa8b55c0a86eb102ab826a46))
* knowledge graph schema, entity resolution, and MCP tools ([abd92b4](https://github.com/OscillateLabsLLC/engram/commit/abd92b4aafe0087d4e036e401cf233aba1ceb377))


### Bug Fixes

* add UNIQUE constraints and expand test coverage ([2fd6ba7](https://github.com/OscillateLabsLLC/engram/commit/2fd6ba7fa6372c5a3b082c5ba36c1d30e7f540ea))
* use string normalization for entity resolution instead of embeddings ([6d2ace7](https://github.com/OscillateLabsLLC/engram/commit/6d2ace7c661d9cc94ebd2a4d0dcfd8657eec140f))

## [2.4.0](https://github.com/OscillateLabsLLC/engram/compare/v2.3.0...v2.4.0) (2026-03-23)


### Features

* keyword search ILIKE fallback for numeric tokens ([5f6b318](https://github.com/OscillateLabsLLC/engram/commit/5f6b318bb5faaa8c50ad5d90ea73d7094d4b2c14))
* keyword search ILIKE fallback for numeric tokens ([8fa01cc](https://github.com/OscillateLabsLLC/engram/commit/8fa01cc23e64cb8cad428272de445a04798fb3af))

## [2.3.0](https://github.com/OscillateLabsLLC/engram/compare/v2.2.2...v2.3.0) (2026-03-23)


### Features

* improve search ranking with cosine normalization and tag boosting ([6378845](https://github.com/OscillateLabsLLC/engram/commit/637884533b3411c8976cee057efc462855549d33))
* improve search ranking with cosine normalization and tag boosting ([3729150](https://github.com/OscillateLabsLLC/engram/commit/3729150de52d0651bab13dd38af7171ef26524ad))

## [2.2.2](https://github.com/OscillateLabsLLC/engram/compare/v2.2.1...v2.2.2) (2026-03-15)


### Bug Fixes

* add practical search mode guidance to MCP tool descriptions ([0a7c45d](https://github.com/OscillateLabsLLC/engram/commit/0a7c45d1dfc1f8b7240e7844858feb4e90c48c38))

## [2.2.1](https://github.com/OscillateLabsLLC/engram/compare/v2.2.0...v2.2.1) (2026-03-15)


### Bug Fixes

* FTS extension load race and graceful fallback when unavailable ([46c7b06](https://github.com/OscillateLabsLLC/engram/commit/46c7b0650ebe5636654cb3ce3b408de21ad4ccac))

## [2.2.0](https://github.com/OscillateLabsLLC/engram/compare/v2.1.1...v2.2.0) (2026-03-15)


### Features

* add DuckDB FTS extension and hybrid search mode ([fda1cf7](https://github.com/OscillateLabsLLC/engram/commit/fda1cf76ba2e5a1591dfa163e96c7a9d6f4e7713))

## [2.1.1](https://github.com/OscillateLabsLLC/engram/compare/v2.1.0...v2.1.1) (2026-03-15)


### Bug Fixes

* CI test race and Docker image name casing ([27a50a1](https://github.com/OscillateLabsLLC/engram/commit/27a50a13cc81b6e37bb82d166fd18c94ab007b96))

## [2.1.0](https://github.com/OscillateLabsLLC/engram/compare/v2.0.0...v2.1.0) (2026-03-15)


### Features

* add similarity scores and min_similarity threshold to search ([858e83f](https://github.com/OscillateLabsLLC/engram/commit/858e83ff6e165ab9fb0c32e45d86065f960cf0c4))

## [2.0.0](https://github.com/OscillateLabsLLC/engram/compare/v1.2.3...v2.0.0) (2026-03-15)


### ⚠ BREAKING CHANGES

* v2.0 server-only architecture with stdio proxy

### Features

* v2.0 server-only architecture with stdio proxy ([9969b3d](https://github.com/OscillateLabsLLC/engram/commit/9969b3db467e42ee9ae14719504e5e2b88cf710e))

## [1.2.3](https://github.com/OscillateLabsLLC/engram/compare/v1.2.2...v1.2.3) (2026-03-01)


### Bug Fixes

* better direction for smaller models ([c9d3a12](https://github.com/OscillateLabsLLC/engram/commit/c9d3a12e6edb645b4a38ecae426a361391f2a622))
* don't return embeddings on memory search ([f02b223](https://github.com/OscillateLabsLLC/engram/commit/f02b2233e4edbb1f5e3b4c850c6b755607b7e72c))
* don't return embeddings on memory search ([c9d3a12](https://github.com/OscillateLabsLLC/engram/commit/c9d3a12e6edb645b4a38ecae426a361391f2a622))

## [1.2.2](https://github.com/OscillateLabsLLC/engram/compare/v1.2.1...v1.2.2) (2026-02-17)


### Bug Fixes

* better migration path ([1c88165](https://github.com/OscillateLabsLLC/engram/commit/1c88165b54f597f09503afc2e422e9eb23bfcd21))

## [1.2.1](https://github.com/OscillateLabsLLC/engram/compare/v1.2.0...v1.2.1) (2026-02-17)


### Bug Fixes

* recreate indexes on migration ([851a76c](https://github.com/OscillateLabsLLC/engram/commit/851a76c77787fbdb50279e3c69ff5e2a1af772ec))

## [1.2.0](https://github.com/OscillateLabsLLC/engram/compare/v1.1.1...v1.2.0) (2026-02-17)


### Features

* improve tests, fix bugs, add automated release ([5635164](https://github.com/OscillateLabsLLC/engram/commit/5635164c7d07bb55e641ab1e322263c9d14651f9))
* improve tests, fix bugs, add automated release ([d045860](https://github.com/OscillateLabsLLC/engram/commit/d045860cf4c18f3bff0f8444028457a68f3f3739))
* persistent local server with docker compose ([170307c](https://github.com/OscillateLabsLLC/engram/commit/170307ca374089fa459243449ea350ee796dc05d))
