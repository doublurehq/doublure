# Changelog

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
