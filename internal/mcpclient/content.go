package mcpclient

import "encoding/json"

// Only this package interprets MCP content tags and embedded resource wire
// fields. Retain unrecognized/malformed blocks explicitly instead of letting
// the SDK's closed content union discard the remainder of an otherwise valid
// ordered result. Image preparation remains the shared media package's job.
func decodeBlock(raw json.RawMessage, resourceRead bool) Block {
	unsupported := Block{Kind: BlockUnsupported, Unsupported: raw}
	if resourceRead {
		return decodeResource(raw, unsupported)
	}
	var wire struct {
		Type     string          `json:"type"`
		Text     *string         `json:"text"`
		Data     *[]byte         `json:"data"`
		MIMEType string          `json:"mimeType"`
		Resource json.RawMessage `json:"resource"`
	}
	if json.Unmarshal(raw, &wire) != nil {
		return unsupported
	}
	switch wire.Type {
	case "text":
		if wire.Text != nil {
			return Block{Kind: BlockText, Text: *wire.Text}
		}
	case "image", "audio":
		if wire.Data != nil && wire.MIMEType != "" {
			kind := BlockImage
			if wire.Type == "audio" {
				kind = BlockAudio
			}
			return Block{Kind: kind, Data: *wire.Data, MIMEType: wire.MIMEType}
		}
	case "resource_link":
		var resource Resource
		if json.Unmarshal(raw, &resource) == nil && validName(resource.URI) && validName(resource.Name) {
			return Block{Kind: BlockResourceLink, Resource: resource}
		}
	case "resource":
		return decodeResource(wire.Resource, unsupported)
	}
	return unsupported
}

func decodeResource(raw json.RawMessage, unsupported Block) Block {
	var wire struct {
		Resource
		Text *string `json:"text"`
		Blob *[]byte `json:"blob"`
	}
	if json.Unmarshal(raw, &wire) != nil || !validName(wire.URI) || (wire.Text == nil) == (wire.Blob == nil) {
		return unsupported
	}
	block := Block{Resource: wire.Resource, MIMEType: wire.MIMEType}
	if wire.Text != nil {
		block.Kind, block.Text = BlockResourceText, *wire.Text
	} else {
		block.Kind, block.Data = BlockResourceBlob, *wire.Blob
	}
	return block
}
