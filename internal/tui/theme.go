package tui

import (
	"charm.land/glamour/v2/ansi"
	glamourstyles "charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
)

// The ink palette uses three dark layers: ink for the screen, xuan for panels,
// and brown for borders and separators.
const (
	inkBlackHex    = "#0D0B0A" // 墨黑
	panelBlackHex  = "#1B1613" // 玄
	separatorHex   = "#332921" // 褐
	primaryTextHex = "#F2E9D8" // 米白
	mutedTextHex   = "#8F8477" // 烟灰
	vermilionHex   = "#C1272D" // 朱红
	goldHex        = "#C9A063" // 金
	successHex     = "#7A9471" // 竹青
	warningHex     = "#D98C3D" // 姜黄
	errorHex       = "#A8383D" // 绛
	informationHex = "#5B8A9E" // 石青
)

var (
	inkBlackColor    = lipgloss.Color(inkBlackHex)
	panelBlackColor  = lipgloss.Color(panelBlackHex)
	primaryTextColor = lipgloss.Color(primaryTextHex)
	mutedTextColor   = lipgloss.Color(mutedTextHex)
	accentColor      = lipgloss.Color(vermilionHex)
	secondaryColor   = lipgloss.Color(goldHex)
	subtleColor      = lipgloss.Color(separatorHex)
	successColor     = lipgloss.Color(successHex)
	warningColor     = lipgloss.Color(warningHex)
	errorColor       = lipgloss.Color(errorHex)
	informationColor = lipgloss.Color(informationHex)

	brandStyle    = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	headerStyle   = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	labelStyle    = lipgloss.NewStyle().Bold(true).Foreground(secondaryColor)
	bodyStyle     = lipgloss.NewStyle().Foreground(primaryTextColor)
	mutedStyle    = lipgloss.NewStyle().Foreground(mutedTextColor)
	infoStyle     = lipgloss.NewStyle().Foreground(informationColor)
	toolNameStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(informationColor)
	userStyle = lipgloss.NewStyle().
			BorderLeft(true).
			BorderStyle(lipgloss.ThickBorder()).
			BorderForeground(secondaryColor).
			Background(panelBlackColor).
			Foreground(primaryTextColor).
			PaddingLeft(1)
	thinkingStyle = lipgloss.NewStyle().
			BorderLeft(true).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(informationColor).
			Background(panelBlackColor).
			Foreground(mutedTextColor).
			PaddingLeft(1)
	composerFocusedStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(accentColor).
				Background(panelBlackColor).
				Padding(0, 1)
	composerBlurredStyle = composerFocusedStyle.BorderForeground(subtleColor)
	errorStyle           = lipgloss.NewStyle().Foreground(errorColor)
	noticeStyle          = lipgloss.NewStyle().Foreground(warningColor)
)

func inkMarkdownStyle() ansi.StyleConfig {
	style := glamourstyles.DarkStyleConfig
	style.Document.Color = stringPointer(primaryTextHex)
	style.BlockQuote.Color = stringPointer(mutedTextHex)
	style.Heading.Color = stringPointer(vermilionHex)
	style.H1.Color = stringPointer(vermilionHex)
	style.H1.BackgroundColor = stringPointer(panelBlackHex)
	style.H6.Color = stringPointer(goldHex)
	style.HorizontalRule.Color = stringPointer(separatorHex)
	style.Item.Color = stringPointer(goldHex)
	style.Enumeration.Color = stringPointer(goldHex)
	style.Task.Color = stringPointer(goldHex)
	style.Link.Color = stringPointer(informationHex)
	style.LinkText.Color = stringPointer(goldHex)
	style.Image.Color = stringPointer(informationHex)
	style.ImageText.Color = stringPointer(mutedTextHex)
	style.Code.Color = stringPointer(goldHex)
	style.Code.BackgroundColor = stringPointer(panelBlackHex)
	style.CodeBlock.Color = stringPointer(primaryTextHex)
	style.Table.Color = stringPointer(primaryTextHex)
	style.DefinitionTerm.Color = stringPointer(vermilionHex)
	style.DefinitionDescription.Color = stringPointer(mutedTextHex)

	if style.CodeBlock.Chroma != nil {
		chroma := *style.CodeBlock.Chroma
		chroma.Text.Color = stringPointer(primaryTextHex)
		chroma.Error.Color = stringPointer(errorHex)
		chroma.Error.BackgroundColor = nil
		chroma.Comment.Color = stringPointer(mutedTextHex)
		chroma.CommentPreproc.Color = stringPointer(warningHex)
		chroma.Keyword.Color = stringPointer(vermilionHex)
		chroma.KeywordReserved.Color = stringPointer(vermilionHex)
		chroma.KeywordNamespace.Color = stringPointer(vermilionHex)
		chroma.KeywordType.Color = stringPointer(informationHex)
		chroma.Operator.Color = stringPointer(goldHex)
		chroma.Punctuation.Color = stringPointer(mutedTextHex)
		chroma.Name.Color = stringPointer(primaryTextHex)
		chroma.NameBuiltin.Color = stringPointer(informationHex)
		chroma.NameTag.Color = stringPointer(vermilionHex)
		chroma.NameAttribute.Color = stringPointer(goldHex)
		chroma.NameClass.Color = stringPointer(informationHex)
		chroma.NameConstant.Color = stringPointer(goldHex)
		chroma.NameDecorator.Color = stringPointer(warningHex)
		chroma.NameException.Color = stringPointer(errorHex)
		chroma.NameFunction.Color = stringPointer(successHex)
		chroma.Literal.Color = stringPointer(goldHex)
		chroma.LiteralNumber.Color = stringPointer(warningHex)
		chroma.LiteralDate.Color = stringPointer(warningHex)
		chroma.LiteralString.Color = stringPointer(successHex)
		chroma.LiteralStringEscape.Color = stringPointer(goldHex)
		chroma.GenericDeleted.Color = stringPointer(errorHex)
		chroma.GenericInserted.Color = stringPointer(successHex)
		chroma.GenericSubheading.Color = stringPointer(mutedTextHex)
		chroma.Background.BackgroundColor = stringPointer(panelBlackHex)
		style.CodeBlock.Chroma = &chroma
	}

	return style
}

func stringPointer(value string) *string {
	return &value
}
