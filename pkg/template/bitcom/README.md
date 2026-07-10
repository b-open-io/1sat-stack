# BitCom Protocols

This template provides Go implementations of Bitcoin BitCom protocols:

- **B** - For binary data and content attachments
- **MAP** - Magic Attribute Protocol for structured key-value data
- **AIP** - Author Identity Protocol for signing data

## Installation

```bash
go get github.com/bitcoin-sv/go-templates/template/bitcom
```

## Available Protocols

### B Protocol

The B protocol allows attaching binary data or content to Bitcoin transactions.

```go
import "github.com/bitcoin-sv/go-templates/template/bitcom"

// Create a B data structure
bData := bitcom.B{
    MediaType: bitcom.MediaTypeTextMarkdown,
    Encoding:  bitcom.EncodingUTF8,
    Data:      []byte("# Hello Bitcoin"),
    Filename:  "hello.md", // Optional
}

// Decode B data from a script
s := &script.Script{} // Assuming this is a script containing B data
decodedB := bitcom.DecodeB(s)
if decodedB != nil {
    // Use the decoded B data
    contentType := decodedB.MediaType
    content := decodedB.Data
}
```

### MAP Protocol

The Magic Attribute Protocol (MAP) allows storing structured key-value data.

```go
import "github.com/bitcoin-sv/go-templates/template/bitcom"

// Decode MAP data from a script
s := &script.Script{} // Assuming this is a script containing MAP data
mapData := bitcom.DecodeMap(s)
if mapData != nil {
    // Access data by key
    app := mapData.Data["app"]
    typeValue := mapData.Data["type"]
}

// Create MAP data
mapScript := &script.Script{}
_ = mapScript.AppendOpcodes(script.OpFALSE, script.OpRETURN)
_ = mapScript.AppendPushDataString(bitcom.MapPrefix)
_ = mapScript.AppendPushDataString("SET")
_ = mapScript.AppendPushDataString("app")
_ = mapScript.AppendPushDataString("myapp")
_ = mapScript.AppendPushDataString("type")
_ = mapScript.AppendPushDataString("post")
```

### AIP Protocol

The Author Identity Protocol (AIP) allows signing data with an identity.

```go
import (
    "github.com/bitcoin-sv/go-templates/template/bitcom"
    ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
)

// Sign data with AIP
privKey, _ := ec.NewPrivateKey()
data := "Data to sign"
sig, err := bsocial.SignAIP(privKey,  data)
if err != nil {
    // Handle error
}

// Create AIP structure
aipData := bitcom.AIP{
    Algorithm: "BITCOIN_ECDSA",
    Address:   "1address...",
    Signature: sig.Signature,
}

// Decode AIP data from a Bitcom structure
bc := &bitcom.Bitcom{} // Assuming this is populated with protocols
aipData := bitcom.DecodeAIP(bc)
```

## Putting It All Together

```go
import (
    "github.com/bitcoin-sv/go-templates/template/bitcom"
    "github.com/bsv-blockchain/go-sdk/script"
)

// Create a Bitcoin script with multiple protocols
s := &script.Script{}
_ = s.AppendOpcodes(script.OpFALSE, script.OpRETURN)

// Add B data
_ = s.AppendPushDataString(bitcom.BPrefix)
_ = s.AppendPushData([]byte("My content"))
_ = s.AppendPushDataString(string(bitcom.MediaTypeTextPlain))
_ = s.AppendPushDataString(string(bitcom.EncodingUTF8))

// Add separator
_ = s.AppendPushDataString("|")

// Add MAP data
_ = s.AppendPushDataString(bitcom.MapPrefix)
_ = s.AppendPushDataString("SET")
_ = s.AppendPushDataString("app")
_ = s.AppendPushDataString("myapp")

// Decode all protocols from a script
bc := bitcom.Decode(s)
if bc != nil {
    // Process protocols
    for _, proto := range bc.Protocols {
        // Handle each protocol based on its type
        switch proto.Protocol {
        case bitcom.BPrefix:
            bData := bitcom.DecodeB(proto.Script)
            // Use B data
        case bitcom.MapPrefix:
            mapData := bitcom.DecodeMap(proto.Script)
            // Use MAP data
        case bitcom.AIPPrefix:
            // AIP is decoded from the entire Bitcom structure
            // using bitcom.DecodeAIP(bc)
        }
    }
}
```

## Constants and Types

```go
// Media Types
const (
    MediaTypeJSON           MediaType = "application/json"
    MediaTypeTextPlain      MediaType = "text/plain"
    MediaTypeTextMarkdown   MediaType = "text/markdown"
    MediaTypeImagePNG       MediaType = "image/png"
    MediaTypeImageJPEG      MediaType = "image/jpeg"
    MediaTypeImageGIF       MediaType = "image/gif"
    MediaTypeImageSVG       MediaType = "image/svg+xml"
    MediaTypeImageWEBP      MediaType = "image/webp"
    MediaTypeAudioMP3       MediaType = "audio/mpeg"
    MediaTypeAudioMP4       MediaType = "audio/mp4"
    MediaTypeAudioWAV       MediaType = "audio/wav"
    MediaTypeVideoMP4       MediaType = "video/mp4"
    MediaTypeApplicationOGG MediaType = "application/ogg"
    MediaTypeApplicationPDF MediaType = "application/pdf"
)

// Encodings
const (
    EncodingUTF8   Encoding = "utf-8"
    EncodingHex    Encoding = "hex"
    EncodingBase64 Encoding = "base64"
)

// Protocol Prefixes
const (
    BPrefix   = "19HxigV4QyBv3tHpQVcUEQyq1pzZVdoAut"
    MapPrefix = "1PuQa7K62MiKCtssSLKy1kh56WWU7MtUR5"
    AIPPrefix = "15PciHG22SNLQJXMoSUaWVi7WSqc7hCfva"
)
```

## Related Resources

- [BitcoinSchema.org](https://bitcoinschema.org/) - Standards for on-chain data formats
- [go-map](https://github.com/BitcoinSchema/go-map) - Go implementation of MAP
- [go-aip](https://github.com/BitcoinSchema/go-aip) - Go implementation of AIP
- [go-b](https://github.com/BitcoinSchema/go-b) - Go implementation of B 