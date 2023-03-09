# Android OTA Payload Extractor

Android OTA payload extractor written in Go.

[Download](https://github.com/tobyxdd/android-ota-payload-extractor/releases)

[中文](README.zh.md)

## Usage

```
./android-ota-payload-extractor <OTA.zip or payload.bin> [(optional) file to extract 1] [(optional) file to extract 2] ...
```

Example (extract boot and vendor images from raven-ota.zip):

```
./android-ota-payload-extractor.exe raven-ota.zip boot vendor
```

It can parse `payload.bin` directly, or automatically extract from OTA zip files.

When files to extract are not specified, it will extract all files in the payload.

![Demo GIF](demo.gif)

## About

Inspired by https://github.com/cyxx/extract_android_ota_payload.

Extracting images from Android OTA packages is very useful for various purposes. For example, patching the boot
image to install Magisk without TWRP. However, using the above Python script is often a pain in the ass because of
Python and its dependencies, especially for inexperienced Windows users.

This project aims to provide everyone with a faster & cross-platform Android OTA payload extractor that's much easier to
use - just drag and drop the ROM file onto the program.