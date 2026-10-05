package mcpclient

import (
	"bytes"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The SDK owns content decoding. This boundary only maps SDK values into the
// transport-neutral types consumed by tool adapters. Image preparation belongs
// to the shared media package.
func contentBlock(content mcp.Content) Block {
	switch content := content.(type) {
	case *mcp.TextContent:
		return Block{Kind: BlockText, Text: content.Text}
	case *mcp.ImageContent:
		return Block{Kind: BlockImage, Data: bytes.Clone(content.Data), MIMEType: content.MIMEType}
	case *mcp.AudioContent:
		return Block{Kind: BlockAudio, Data: bytes.Clone(content.Data), MIMEType: content.MIMEType}
	case *mcp.ResourceLink:
		return Block{Kind: BlockResourceLink, Resource: Resource{
			URI: content.URI, Name: content.Name, Title: content.Title,
			Description: content.Description, MIMEType: content.MIMEType,
		}}
	case *mcp.EmbeddedResource:
		return resourceBlock(content.Resource)
	default:
		return Block{Kind: BlockUnsupported}
	}
}

func resourceBlock(content *mcp.ResourceContents) Block {
	// One block cannot represent both text and binary without silently dropping
	// one value. Let the adapter report this as source loss.
	if content == nil || content.Blob != nil && content.Text != "" {
		return Block{Kind: BlockUnsupported}
	}
	block := Block{Resource: Resource{URI: content.URI, MIMEType: content.MIMEType}, MIMEType: content.MIMEType}
	if content.Blob != nil {
		block.Kind, block.Data = BlockResourceBlob, bytes.Clone(content.Blob)
	} else {
		block.Kind, block.Text = BlockResourceText, content.Text
	}
	return block
}
