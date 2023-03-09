package main

import (
	"archive/zip"
	"bytes"
	"compress/bzip2"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/golang/protobuf/proto"
	"github.com/xi2/xz"
)

const (
	payloadFilename = "payload.bin"

	zipMagic     = "PK"
	payloadMagic = "CrAU"

	brilloMajorPayloadVersion = 2
)

func main() {
	if len(os.Args) < 2 {
		fmt.Printf("Usage: %s <input> [(optional) file to extract...]\n", os.Args[0])
		os.Exit(1)
	}
	filename := os.Args[1]
	extractFiles := os.Args[2:]
	f, err := os.Open(filename)
	if err != nil {
		log.Fatalf("Failed to open file: %s", err)
	}
	if isZip(f) {
		// Extract payload.bin from the zip first
		_ = f.Close()
		log.Printf("Input is a zip file, searching for %s ...\n", payloadFilename)
		zr, err := zip.OpenReader(filename)
		if err != nil {
			log.Fatalf("Failed to open the zip file: %s\n", err.Error())
		}
		zf, err := findPayload(zr)
		if err != nil {
			log.Fatalf("Failed to read from the zip file: %s\n", err.Error())
		}
		if zf == nil {
			log.Fatalf("%s not found in the zip file\n", payloadFilename)
		}
		log.Printf("Extracting %s ...\n", payloadFilename)
		f, err = os.Create(payloadFilename)
		if err != nil {
			log.Fatalf("Failed to create the extraction file: %s\n", err.Error())
		}
		_, err = io.Copy(f, zf)
		if err != nil {
			log.Fatalf("Failed to extract: %s\n", err.Error())
		}
		_ = zf.Close()
		_ = zr.Close()
		_, _ = f.Seek(0, 0)
	}
	parsePayload(f, extractFiles)
}

func isZip(f *os.File) bool {
	header := make([]byte, len(zipMagic))
	_, err := f.Read(header)
	_, _ = f.Seek(0, 0)
	return err == nil && string(header) == zipMagic
}

func findPayload(zr *zip.ReadCloser) (io.ReadCloser, error) {
	for _, f := range zr.File {
		if f.Name == payloadFilename {
			return f.Open()
		}
	}
	return nil, nil
}

func parsePayload(r io.ReadSeeker, extractFiles []string) {
	log.Println("Parsing payload...")
	// magic
	magic := make([]byte, len(payloadMagic))
	_, err := r.Read(magic)
	if err != nil || string(magic) != payloadMagic {
		log.Fatalf("Incorrect magic (%s)\n", string(magic))
	}
	// version & lengths
	var version, manifestLen uint64
	var metadataSigLen uint32
	err = binary.Read(r, binary.BigEndian, &version)
	if err != nil || version != brilloMajorPayloadVersion {
		log.Fatalf("Unsupported payload version (%d). This tool only supports version %d\n",
			version, brilloMajorPayloadVersion)
	}
	err = binary.Read(r, binary.BigEndian, &manifestLen)
	if err != nil || !(manifestLen > 0) {
		log.Fatalf("Incorrect manifest length (%d)\n", manifestLen)
	}
	err = binary.Read(r, binary.BigEndian, &metadataSigLen)
	if err != nil || !(metadataSigLen > 0) {
		log.Fatalf("Incorrect metadata signature length (%d)\n", metadataSigLen)
	}
	// manifest
	manifestRaw := make([]byte, manifestLen)
	n, err := r.Read(manifestRaw)
	if err != nil || uint64(n) != manifestLen {
		log.Fatalf("Failed to read the manifest (%d)\n", manifestLen)
	}
	var manifest DeltaArchiveManifest
	err = proto.Unmarshal(manifestRaw, &manifest)
	if err != nil {
		log.Fatalf("Failed to parse the manifest: %s\n", err.Error())
	}
	// only support full payloads!
	if *manifest.MinorVersion != 0 {
		log.Fatalf("Delta payloads are not supported, please use a full payload file\n")
	}
	// print manifest info
	log.Printf("Block size: %d, Partition count: %d\n",
		*manifest.BlockSize, len(manifest.Partitions))
	// extract partitions
	extractPartitions(&manifest, r, 24+manifestLen+uint64(metadataSigLen), extractFiles)
	// done
	log.Println("Done!")
}

func extractPartitions(manifest *DeltaArchiveManifest, r io.ReadSeeker, baseOffset uint64, extractFiles []string) {
	for _, p := range manifest.Partitions {
		if p.PartitionName == nil || (len(extractFiles) > 0 && !contains(extractFiles, *p.PartitionName)) {
			continue
		}
		log.Printf("Extracting %s (%d ops) ...", *p.PartitionName, len(p.Operations))
		outFilename := fmt.Sprintf("%s.img", *p.PartitionName)
		extractPartition(p, outFilename, r, baseOffset, *manifest.BlockSize)
	}
}

func extractPartition(p *PartitionUpdate, outFilename string, r io.ReadSeeker, baseOffset uint64, blockSize uint32) {
	outFile, err := os.Create(outFilename)
	if err != nil {
		log.Fatalf("Failed to create the output file: %s\n", err.Error())
	}
	for _, op := range p.Operations {
		data, dataPos := make([]byte, *op.DataLength), int64(baseOffset+*op.DataOffset)

		_, err = r.Seek(dataPos, 0)
		if err != nil {
			_ = outFile.Close()
			log.Fatalf("Failed to seek to %d in partition %s: %s\n", dataPos, outFilename, err.Error())
		}
		n, err := r.Read(data)
		if err != nil || uint64(n) != *op.DataLength {
			_ = outFile.Close()
			log.Fatalf("Failed to read enough data from partition %s: %s\n", outFilename, err.Error())
		}

		outSeekPos := int64(*op.DstExtents[0].StartBlock * uint64(blockSize))
		_, err = outFile.Seek(outSeekPos, 0)
		if err != nil {
			_ = outFile.Close()
			log.Fatalf("Failed to seek to %d in partition %s: %s\n", outSeekPos, outFilename, err.Error())
		}

		switch *op.Type {
		case InstallOperation_REPLACE:
			_, err = outFile.Write(data)
			if err != nil {
				_ = outFile.Close()
				log.Fatalf("Failed to write output to %s: %s\n", outFilename, err.Error())
			}
		case InstallOperation_REPLACE_BZ:
			bzr := bzip2.NewReader(bytes.NewReader(data))
			_, err = io.Copy(outFile, bzr)
			if err != nil {
				_ = outFile.Close()
				log.Fatalf("Failed to write output to %s: %s\n", outFilename, err.Error())
			}
		case InstallOperation_REPLACE_XZ:
			xzr, err := xz.NewReader(bytes.NewReader(data), 0)
			if err != nil {
				_ = outFile.Close()
				log.Fatalf("Bad xz data in partition %s: %s\n", *p.PartitionName, err.Error())
			}
			_, err = io.Copy(outFile, xzr)
			if err != nil {
				_ = outFile.Close()
				log.Fatalf("Failed to write output to %s: %s\n", outFilename, err.Error())
			}
		case InstallOperation_ZERO:
			for _, ext := range op.DstExtents {
				outSeekPos = int64(*ext.StartBlock * uint64(blockSize))
				_, err = outFile.Seek(outSeekPos, 0)
				if err != nil {
					_ = outFile.Close()
					log.Fatalf("Failed to seek to %d in partition %s: %s\n", outSeekPos, outFilename, err.Error())
				}
				// write zeros
				_, err = io.Copy(outFile, bytes.NewReader(make([]byte, *ext.NumBlocks*uint64(blockSize))))
				if err != nil {
					_ = outFile.Close()
					log.Fatalf("Failed to write output to %s: %s\n", outFilename, err.Error())
				}
			}
		default:
			_ = outFile.Close()
			log.Fatalf("Unsupported operation type: %d (%s), please report a bug\n",
				*op.Type, InstallOperation_Type_name[int32(*op.Type)])
		}
	}
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
