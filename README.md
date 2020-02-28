# Android OTA Payload Extractor

Android OTA payload extractor written in Go.

[Download](https://github.com/tobyxdd/android-ota-payload-extractor/releases)

[中文](README.zh.md)

## Usage

```
./android-ota-payload-extractor <OTA.zip or payload.bin>
```

It can parse `payload.bin` directly, or automatically extract from OTA zip files.

![Demo GIF](demo.gif)

## About

Inspired by https://github.com/cyxx/extract_android_ota_payload.

Extracting images from Android OTA packages is extremely useful for various purposes. For example, patching the boot image to install Magisk without TWRP. Using the python script above, however, is often a pain in the ass because of Python and its dependencies, especially for inexperienced Windows users.

This project aims to provide everyone a faster & cross-platform Android OTA payload extractor that's a lot easier to use - just drag and drop the ROM file onto the program.