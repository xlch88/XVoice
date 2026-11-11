package asr

import (
	"time"

	"github.com/xlch88/XVoice/internal/config"
)

// Languages are the recognition models offered in settings, named as in the
// official client. Every entry was probed against the live service and
// measurably switches the model. The service silently serves values it does
// not know with the default model, so the official client's other languages
// (Russian, Thai, Vietnamese, French, German, Spanish, Portuguese, Italian,
// Arabic, Yi, Zhuang), which this endpoint ignores, are left out.
var Languages = []config.Choice{
	{Value: "", Name: "普通话"},
	{Value: "cantonese-ed16k", Name: "粤语普通话免切换"},
	{Value: "sms-en16k", Name: "英语"},
	{Value: "zhongchuan", Name: "四川话普通话免切换"},
	{Value: "henanese16k", Name: "河南话"},
	{Value: "dongbeiese16k", Name: "东北话"},
	{Value: "tianjinese16k", Name: "天津话"},
	{Value: "changshanese16k", Name: "湖南话（长沙）"},
	{Value: "shandongnese16k", Name: "山东话（济南）"},
	{Value: "wuhanese16k", Name: "武汉话"},
	{Value: "hefeinese16k", Name: "合肥话"},
	{Value: "nanchangnese16k", Name: "江西话（南昌）"},
	{Value: "hebeinese16k", Name: "河北话"},
	{Value: "minnanese16k", Name: "闽南语"},
	{Value: "hakkanese16k", Name: "客家语"},
	{Value: "guizhounese16k", Name: "贵州话"},
	{Value: "gansunese16k", Name: "甘肃话"},
	{Value: "ningxianese16k", Name: "宁夏话"},
	{Value: "taiwanese", Name: "国语（台湾）"},
	{Value: "yunnanese16k", Name: "云南话（昆明）"},
	{Value: "taiyuanese16k", Name: "山西话（太原）"},
	{Value: "shanxinese16k", Name: "陕西话（西安）"},
	{Value: "wanbeinese", Name: "皖北话"},
	{Value: "shanghainese16k", Name: "上海话"},
	{Value: "suzhounese", Name: "苏州话"},
	{Value: "nankinese16k", Name: "南京话"},
	{Value: "tibetan16k", Name: "藏语"},
	{Value: "uyghur16k", Name: "维吾尔语"},
	{Value: "korean", Name: "朝鲜语"},
	{Value: "ja_jp", Name: "日语"},
	{Value: "ko_kr", Name: "韩语"},
}

// Domains bias Mandarin recognition toward a vocabulary, as "领域识别优化" does.
var Domains = []config.Choice{
	{Value: "", Name: "通用"},
	{Value: "shopping", Name: "购物"},
	{Value: "medical", Name: "医疗"},
	{Value: "game", Name: "游戏"},
	{Value: "trip", Name: "旅游"},
}

// Options select the recognition model; the zero value is general Mandarin
// dictated continuously.
type Options struct {
	Language string // A Languages value.
	Domain   string // A Domains value; only Mandarin honors it.
	// EndPause ends recognition once the speaker has said something and then
	// stayed silent this long. Zero keeps listening: the service still closes
	// each utterance after its default ~1.8 s pause, and a new connection
	// picks up the next one.
	EndPause time.Duration
}

// business builds the first frame's model parameters the way the official
// client does. The service ignores "ent" whenever "language" or "accent" is
// present, so the two families must not be mixed.
func (o Options) business() map[string]any {
	b := map[string]any{"aue": "raw", "dwa": "wpgs", "vad_enable": false}
	if o.EndPause > 0 {
		// Measured: the service finalizes and closes eos ms after speech stops.
		b["eos"] = o.EndPause.Milliseconds()
	}
	switch o.Language {
	case "ja_jp", "ko_kr":
		b["language"], b["domain"], b["accent"] = o.Language, "iat", "mandarin"
	case "":
		b["ent"] = "sms16k"
		if o.Domain != "" {
			b["pd"] = o.Domain
		}
	default:
		b["ent"] = o.Language
	}
	return b
}
