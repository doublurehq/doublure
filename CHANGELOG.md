# Changelog

## [0.10.0](https://github.com/doublurehq/doublure/compare/v0.9.0...v0.10.0) (2026-10-28)


### Features

* cap preview slugs and hosts to their dns limits ([#40](https://github.com/doublurehq/doublure/issues/40)) ([51d2957](https://github.com/doublurehq/doublure/commit/51d29577f6983d7b1ad61fae60e81312a47124f1))

## [0.9.0](https://github.com/doublurehq/doublure/compare/v0.8.1...v0.9.0) (2026-10-28)


### Features

* **cli:** create the github app with a given webhook url ([#38](https://github.com/doublurehq/doublure/issues/38)) ([06f6856](https://github.com/doublurehq/doublure/commit/06f685673656ce5a1909125ff9ed1e4f67f414c3))

## [0.8.1](https://github.com/doublurehq/doublure/compare/v0.8.0...v0.8.1) (2026-10-28)


### Bug Fixes

* **lifecycle:** skip the empty branch slug in payload teardown ([#36](https://github.com/doublurehq/doublure/issues/36)) ([7388e76](https://github.com/doublurehq/doublure/commit/7388e76e9558f3250c6c2f829faa02c7169af070))

## [0.8.0](https://github.com/doublurehq/doublure/compare/v0.7.0...v0.8.0) (2026-10-28)


### Features

* share a preview across pull requests with named labels ([#34](https://github.com/doublurehq/doublure/issues/34)) ([aa1d3df](https://github.com/doublurehq/doublure/commit/aa1d3dfba31407c1e8e5362d7f5a4f23775ae01e))

## [0.7.0](https://github.com/doublurehq/doublure/compare/v0.6.0...v0.7.0) (2026-10-28)


### ⚠ BREAKING CHANGES

* create previews from a pull request label ([#31](https://github.com/doublurehq/doublure/issues/31))

### Features

* create previews from a pull request label ([#31](https://github.com/doublurehq/doublure/issues/31)) ([3062e7b](https://github.com/doublurehq/doublure/commit/3062e7ba11ecf126d11d96984a416ab5653b3f8b))

## [0.6.0](https://github.com/doublurehq/doublure/compare/v0.5.0...v0.6.0) (2026-10-27)


### Features

* add a deploy action for service ci ([#29](https://github.com/doublurehq/doublure/issues/29)) ([8e3eb94](https://github.com/doublurehq/doublure/commit/8e3eb94cda11d5b94425ebb365cb624fbe33108c))

## [0.5.0](https://github.com/doublurehq/doublure/compare/v0.4.1...v0.5.0) (2026-10-27)


### ⚠ BREAKING CHANGES

* make the app chart data-agnostic ([#27](https://github.com/doublurehq/doublure/issues/27))

### Features

* make the app chart data-agnostic ([#27](https://github.com/doublurehq/doublure/issues/27)) ([37905a2](https://github.com/doublurehq/doublure/commit/37905a23f5d39dfc0cdeaa6ca6f96ea4fc317cdd))

## [0.4.1](https://github.com/doublurehq/doublure/compare/v0.4.0...v0.4.1) (2026-10-27)


### Bug Fixes

* run the cleanup job once per schedule ([#25](https://github.com/doublurehq/doublure/issues/25)) ([ebfa068](https://github.com/doublurehq/doublure/commit/ebfa068322b6f8e7096826cf8cb38f2119908cea))

## [0.4.0](https://github.com/doublurehq/doublure/compare/v0.3.0...v0.4.0) (2026-10-27)


### Features

* sweep stale and expired previews ([#23](https://github.com/doublurehq/doublure/issues/23)) ([1fed83d](https://github.com/doublurehq/doublure/commit/1fed83d6b0e76389c9bef8bfd76a47536ff37e7c))

## [0.3.0](https://github.com/doublurehq/doublure/compare/v0.2.1...v0.3.0) (2026-10-27)


### Features

* tear down previews when a pull request closes ([#21](https://github.com/doublurehq/doublure/issues/21)) ([2db444a](https://github.com/doublurehq/doublure/commit/2db444aa0ab9b2346513eaf3233b1129b2e0c5ab))

## [0.2.1](https://github.com/doublurehq/doublure/compare/v0.2.0...v0.2.1) (2026-10-27)


### Bug Fixes

* quote preview metadata safely and replace hand-rolled helpers ([#19](https://github.com/doublurehq/doublure/issues/19)) ([8aee7db](https://github.com/doublurehq/doublure/commit/8aee7db82ba00fded045853e8a7d9b5aa6ab5032))

## [0.2.0](https://github.com/doublurehq/doublure/compare/v0.1.1...v0.2.0) (2026-10-27)


### Features

* show per-preview urls for exposed services ([#16](https://github.com/doublurehq/doublure/issues/16)) ([8c13e76](https://github.com/doublurehq/doublure/commit/8c13e76b399af7bcbb247841e0505dc6b8366e47))

## [0.1.1](https://github.com/doublurehq/doublure/compare/v0.1.0...v0.1.1) (2026-10-27)


### Bug Fixes

* remove internal leftovers from docs and dev defaults ([#14](https://github.com/doublurehq/doublure/issues/14)) ([27ac922](https://github.com/doublurehq/doublure/commit/27ac922f8b56ac77aef85bb87c4300461536e879))

## 0.1.0 (2026-10-27)


### Features

* **charts:** add preview-env, app and doublure helm charts ([79a969e](https://github.com/doublurehq/doublure/commit/79a969e1dc6428ed0eeafea04109f3c4ad6ade0e))
* **cli:** add serve, verify-app and bootstrap-app commands ([6a30f65](https://github.com/doublurehq/doublure/commit/6a30f651ff086c676765c82caaef17244403d033))
* **config:** load configuration from the environment ([765212f](https://github.com/doublurehq/doublure/commit/765212f0a0d5b6178c9bafefffa6eceb11a7c303))
* **github:** authenticate as a github app ([9ef80c8](https://github.com/doublurehq/doublure/commit/9ef80c8c9980eace036582c9f2bb99402bf99888))
* **gitops:** write preview values with optimistic locking ([17be26a](https://github.com/doublurehq/doublure/commit/17be26a4f1555a787d71246f37cdc04d0469dd76))
* **preview:** compute preview slugs, values and pr summaries ([e566536](https://github.com/doublurehq/doublure/commit/e56653689050571ce6ebbb727e1951e5a793ea5e))
* **server:** serve the deploy endpoint and github webhooks ([90bc907](https://github.com/doublurehq/doublure/commit/90bc9070f4bd3faac06d47d09763746aa5472732))
