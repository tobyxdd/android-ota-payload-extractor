package main

import (
	"archive/zip"
	"bytes"
	"compress/bzip2"
	"encoding/binary"
	"fmt"
	"github.com/golang/protobuf/proto"
	"github.com/xi2/xz"
	"io"
	"log"
	"os"
	"sync"
)

const (
	payloadFilename = "payload.bin"

	zipMagic     = "PK"
	payloadMagic = "CrAU"

	brilloMajorPayloadVersion = 2

	blockSize = uint64(4096)
)

var seekReadMutex sync.Mutex
var wg sync.WaitGroup

func main() {
	if len(os.Args) < 2 {
		log.Fatalln("not enough arguments")
	}
	filename := os.Args[1]
	f, err := os.Open(filename)
	if err != nil {
		log.Fatalf("unable to open file %s: %s\n", filename, err.Error())
	}
	if isZip(f) {
		// extract payload.bin from the zip first
		_ = f.Close()
		log.Printf("input recognized as a zip file. searching for %s ...\n", payloadFilename)
		zr, err := zip.OpenReader(filename)
		if err != nil {
			log.Fatalf("unable to open zip file %s: %s\n", filename, err.Error())
		}
		zf, err := findPayload(zr)
		if err != nil {
			log.Fatalf("unable to read from the zip file: %s\n", err.Error())
		}
		if zf == nil {
			log.Fatalf("%s not found in the zip file\n", payloadFilename)
		}
		log.Printf("%s found, extracting...\n", payloadFilename)
		pf, err := os.Create(payloadFilename)
		if err != nil {
			log.Fatalf("unable to create extraction file %s: %s\n", payloadFilename, err.Error())
		}
		_, err = io.Copy(pf, zf)
		if err != nil {
			log.Fatalf("extraction failed: %s\n", err.Error())
		}
		_ = zf.Close()
		_ = zr.Close()
		_, _ = pf.Seek(0, 0)
		parsePayload(pf)
	} else {
		parsePayload(f)
	}
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

func parsePayload(r io.ReadSeeker) {
	log.Println("parsing payload...")
	// magic
	magic := make([]byte, len(payloadMagic))
	_, err := r.Read(magic)
	if err != nil || string(magic) != payloadMagic {
		log.Fatalf("incorrect magic (%s)\n", string(magic))
	}
	// version & lengths
	var version, manifestLen uint64
	var metadataSigLen uint32
	err = binary.Read(r, binary.BigEndian, &version)
	if err != nil || version != brilloMajorPayloadVersion {
		log.Fatalf("incorrect version (%d)\n", version)
	}
	err = binary.Read(r, binary.BigEndian, &manifestLen)
	if err != nil || !(manifestLen > 0) {
		log.Fatalf("incorrect manifest length (%d)\n", manifestLen)
	}
	err = binary.Read(r, binary.BigEndian, &metadataSigLen)
	if err != nil || !(metadataSigLen > 0) {
		log.Fatalf("incorrect metadata signature length (%d)\n", metadataSigLen)
	}
	log.Printf("version: %d, manifest length: %d, metadata signature length: %d\n",
		version, manifestLen, metadataSigLen)
	// manifest
	manifestRaw := make([]byte, manifestLen)
	n, err := r.Read(manifestRaw)
	if err != nil || uint64(n) != manifestLen {
		log.Fatalf("unable to read manifest (%d)\n", n)
	}
	var manifest DeltaArchiveManifest
	err = proto.Unmarshal(manifestRaw, &manifest)
	if err != nil {
		log.Fatalf("unable to parse manifest: %s\n", err.Error())
	}
	// extract partitions
	extractPartitions(manifest.Partitions, r, 24+manifestLen+uint64(metadataSigLen))
}

func extractPartitions(partitions []*PartitionUpdate, r io.ReadSeeker, baseOffset uint64) {
	log.Printf("%d partitions", len(partitions))
	for _, p := range partitions {
		if p.PartitionName == nil {
			continue
		}
		wg.Add(1)
		go func(p *PartitionUpdate) {
			outFilename := fmt.Sprintf("%s.img", *p.PartitionName)
			extractPartition(p, outFilename, r, baseOffset)
			log.Println(outFilename, "extracted")
			wg.Done()
		}(p)
	}
	wg.Wait()
}

func extractPartition(p *PartitionUpdate, outFilename string, r io.ReadSeeker, baseOffset uint64) {
	outFile, err := os.Create(outFilename)
	if err != nil {
		log.Fatalf("unable to create output file %s: %s\n", outFilename, err.Error())
	}
	for _, op := range p.Operations {
		e := op.DstExtents[0]
		data, dataPos := make([]byte, *op.DataLength), int64(baseOffset+*op.DataOffset)

		seekReadMutex.Lock()
		_, err = r.Seek(dataPos, 0)
		if err != nil {
			seekReadMutex.Unlock()
			_ = outFile.Close()
			log.Fatalf("unable to seek to %d in partition %s\n", dataPos, *p.PartitionName)
		}
		n, err := r.Read(data)
		seekReadMutex.Unlock()

		if err != nil || uint64(n) != *op.DataLength {
			_ = outFile.Close()
			log.Fatalf("unable to read enough data from partition %s\n", *p.PartitionName)
		}
		_, _ = outFile.Seek(int64(*e.StartBlock*blockSize), 0)
		switch *op.Type {
		case InstallOperation_REPLACE:
			_, err = outFile.Write(data)
			if err != nil {
				_ = outFile.Close()
				log.Fatalf("unable to write output to %s: %s\n", outFilename, err.Error())
			}
		case InstallOperation_REPLACE_BZ:
			bzr := bzip2.NewReader(bytes.NewReader(data))
			_, err = io.Copy(outFile, bzr)
			if err != nil {
				_ = outFile.Close()
				log.Fatalf("unable to write output to %s: %s\n", outFilename, err.Error())
			}
		case InstallOperation_REPLACE_XZ:
			xzr, err := xz.NewReader(bytes.NewReader(data), 0)
			if err != nil {
				_ = outFile.Close()
				log.Fatalf("bad xz data in partition %s: %s\n", *p.PartitionName, err.Error())
			}
			_, err = io.Copy(outFile, xzr)
			if err != nil {
				_ = outFile.Close()
				log.Fatalf("unable to write output to %s: %s\n", outFilename, err.Error())
			}
		default:
			_ = outFile.Close()
			log.Fatalf("unsupported operation type: %d (%s)\n", *op.Type, InstallOperation_Type_name[int32(*op.Type)])
		}
	}
}
