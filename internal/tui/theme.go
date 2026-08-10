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
	sunsetHex      = "#FF6B6B" // 霞绯
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
	accentColor      = lipgloss.Color(sunsetHex)
	secondaryColor   = lipgloss.Color(goldHex)
	subtleColor      = lipgloss.Color(separatorHex)
	successColor     = lipgloss.Color(successHex)
	warningColor     = lipgloss.Color(warningHex)
	errorColor       = lipgloss.Color(errorHex)
	informationColor = lipgloss.Color(informationHex)

	brandStyle         = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	headerStyle        = lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	labelStyle         = lipgloss.NewStyle().Bold(true).Foreground(secondaryColor)
	bodyStyle          = lipgloss.NewStyle().Foreground(primaryTextColor)
	assistantBodyStyle = lipgloss.NewStyle().
				PaddingLeft(2)
	mutedStyle             = lipgloss.NewStyle().Foreground(mutedTextColor)
	infoStyle              = lipgloss.NewStyle().Foreground(informationColor)
	pendingSteerLabelStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(informationColor)
	pendingSteerStyle = lipgloss.NewStyle().
				BorderLeft(true).
				BorderStyle(lipgloss.NormalBorder()).
				BorderForeground(informationColor).
				Foreground(informationColor).
				PaddingLeft(1)
	toolNameStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(informationColor)
	userStyle = lipgloss.NewStyle().
			BorderLeft(true).
			BorderStyle(lipgloss.ThickBorder()).
			BorderForeground(secondaryColor).
			Foreground(mutedTextColor).
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
				Padding(0, 1)
	composerBlurredStyle  = composerFocusedStyle.BorderForeground(subtleColor)
	slashCommandMenuStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(subtleColor).
				Padding(0, 1)
	slashCommandRowStyle = lipgloss.NewStyle().
				Foreground(primaryTextColor)
	slashCommandSelectedStyle = slashCommandRowStyle.
					Foreground(accentColor)
	commandOutputStyle = lipgloss.NewStyle().
				Foreground(primaryTextColor).
				BorderLeft(true).
				BorderStyle(lipgloss.NormalBorder()).
				BorderForeground(informationColor).
				PaddingLeft(1)
	transcriptSelectionStyle = lipgloss.NewStyle().
					Foreground(inkBlackColor).
					Background(secondaryColor)
	errorStyle  = lipgloss.NewStyle().Foreground(errorColor)
	noticeStyle = lipgloss.NewStyle().Foreground(warningColor)
)

func inkMarkdownStyle() ansi.StyleConfig {
	style := glamourstyles.DarkStyleConfig
	style.Document.Color = stringPointer(primaryTextHex)
	style.Document.BlockPrefix = ""
	style.Document.BlockSuffix = ""
	style.Document.Margin = uintPointer(0)
	style.BlockQuote.Color = stringPointer(mutedTextHex)
	style.Heading.Color = stringPointer(sunsetHex)
	style.H1.Color = stringPointer(sunsetHex)
	style.H1.BackgroundColor = stringPointer(panelBlackHex)
	style.H6.Color = stringPointer(goldHex)
	style.HorizontalRule.Color = stringPointer(separatorHex)
	style.Item.Color = stringPointer(goldHex)
	style.Enumeration.Color = stringPointer(goldHex)
	style.Task.Color = stringPointer(goldHex)
	style.Task.Ticked = "✅ "
	style.Task.Unticked = "⏳ "
	style.Link.Color = stringPointer(informationHex)
	style.LinkText.Color = stringPointer(goldHex)
	style.Image.Color = stringPointer(informationHex)
	style.ImageText.Color = stringPointer(mutedTextHex)
	style.Code.Color = stringPointer(goldHex)
	style.Code.BackgroundColor = stringPointer(panelBlackHex)
	style.CodeBlock.Color = stringPointer(primaryTextHex)
	style.Table.Color = stringPointer(primaryTextHex)
	style.DefinitionTerm.Color = stringPointer(sunsetHex)
	style.DefinitionDescription.Color = stringPointer(mutedTextHex)

	if style.CodeBlock.Chroma != nil {
		chroma := *style.CodeBlock.Chroma
		chroma.Text.Color = stringPointer(primaryTextHex)
		chroma.Error.Color = stringPointer(errorHex)
		chroma.Error.BackgroundColor = nil
		chroma.Comment.Color = stringPointer(mutedTextHex)
		chroma.CommentPreproc.Color = stringPointer(warningHex)
		chroma.Keyword.Color = stringPointer(sunsetHex)
		chroma.KeywordReserved.Color = stringPointer(sunsetHex)
		chroma.KeywordNamespace.Color = stringPointer(sunsetHex)
		chroma.KeywordType.Color = stringPointer(informationHex)
		chroma.Operator.Color = stringPointer(goldHex)
		chroma.Punctuation.Color = stringPointer(mutedTextHex)
		chroma.Name.Color = stringPointer(primaryTextHex)
		chroma.NameBuiltin.Color = stringPointer(informationHex)
		chroma.NameTag.Color = stringPointer(sunsetHex)
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

func uintPointer(value uint) *uint {
	return &value
}
