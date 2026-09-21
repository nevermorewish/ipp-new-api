package openaigpt

// ModelList is the OpenAI-GPT channel's default model list.
//
// This channel exists to talk to endpoints that implement the official OpenAI
// Responses/Chat contract, so the list is deliberately limited to the model
// families whose official contract this channel enforces. Administrators can
// still add any model to a channel; this list only seeds the UI.
var ModelList = []string{
	"gpt-5.6",
	"gpt-5.6-sol",
	"gpt-5.6-terra",
	"gpt-5.6-luna",
	"gpt-5.4",
	"gpt-5.4-mini",
	"gpt-5",
	"gpt-5-mini",
	"gpt-5-nano",
	"gpt-4.1",
	"gpt-4.1-mini",
	"gpt-4.1-nano",
	"gpt-4o",
	"gpt-4o-mini",
	"o3",
	"o3-mini",
	"o4-mini",
}

// ChannelName identifies this channel in logs, pricing and the model catalog.
const ChannelName = "openaigpt"
