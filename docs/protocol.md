# 识别接口的 WebSocket 协议研究

XVoice 使用的语音识别接口提取自讯飞输入法 PC 客户端（3.0.1750）。这份文档记录这个接口的协议，以及对真实服务端实测得到的行为。实现见 [`internal/asr`](../internal/asr)。

每条结论都标了来源：

- **实测**：用合成语音向真实服务端发请求，对比返回结果得到。
- **静态分析**：来自对官方客户端 `iFlySpeechLogic.dll` 的反汇编，没有经过实测。
- **推断**：根据现象做出的解释，没有直接证据。

接口不是公开接口，服务端随时可能调整，以下内容只在撰写时成立。

## 连接与鉴权

地址：

```text
wss://100ime-iat-api.xfyun.cn/v2/iat
```

鉴权信息放在 URL 的查询参数里，共三个：`host`、`date`、`authorization`。（实测）

1. 取当前时间的 HTTP 日期格式（GMT），作为 `date`。
2. 拼出签名原文，三行，用 `\n` 分隔，末尾没有换行：

   ```text
   host: 100ime-iat-api.xfyun.cn
   date: <date>
   GET /v2/iat HTTP/1.1
   ```

3. 用 appSecret 对签名原文做 HMAC-SHA256，结果做 Base64，得到 `signature`。
4. 拼出鉴权描述，再整体做一次 Base64，作为 `authorization`：

   ```text
   hmac username="<appKey>", algorithm="hmac-sha256", headers="host date request-line", signature="<signature>"
   ```

注意字段名是 `hmac username`，和讯飞开放平台文档里的 `api_key` 写法不同。appKey 和 appSecret 是官方客户端内置的，见 `internal/asr/recognize.go`。

## 客户端发送的消息

所有消息都是 JSON 文本帧。音频为 16 kHz、16 位、单声道 PCM，每帧 1280 字节（40 毫秒），Base64 后放进 `data.audio`。

一个连接里的第一帧带 `common` 和 `business`，之后的帧只有 `data`：

```json
{
  "common": { "app_id": "100IME", "uid": "", "device_id": "<随机十六进制串>" },
  "business": { "ent": "sms16k", "aue": "raw", "dwa": "wpgs", "vad_enable": false },
  "data": { "status": 0, "format": "audio/L16;rate=16000", "encoding": "raw", "audio": "<base64>" }
}
```

`data.status` 的取值：

| 值 | 含义 |
|---|---|
| 0 | 连接里的第一帧 |
| 1 | 后续的音频帧 |
| 2 | 音频结束，`audio` 为空；服务端收到后返回最终结果并结束 |

说明：

- 发送速度可以比实时快。实测按 2 倍速发送，识别结果正常。
- `device_id` 用随机值即可，XVoice 每次听写生成一个新的。（实测）
- 官方客户端发送的是 Opus 或 Speex 编码的音频（`aue` 为 `opus-wb` 或 `speex-wb`），并且首帧的 `status` 是 1 而不是 0。（静态分析）XVoice 用 `raw` 加 `status: 0`，实测可用。

## business 参数

| 字段 | 类型 | 作用 | 来源 |
|---|---|---|---|
| `ent` | 字符串 | 选择识别模型，见下一节 | 实测 |
| `language`、`domain`、`accent` | 字符串 | 另一种选择模型的方式，用于日语、韩语 | 实测 |
| `pd` | 字符串 | 领域优化：`shopping`、`medical`、`game`、`trip`，只和 `ent=sms16k` 搭配 | 服务端接受；效果未验证 |
| `aue` | 字符串 | 音频编码，XVoice 用 `raw` | 实测 |
| `dwa` | 字符串 | 取 `wpgs` 时开启边说边出结果、并允许修正前面的内容 | 实测 |
| `eos` | 数字 | 说话停止后多少毫秒结束本次识别 | 实测 |
| `vad_enable` | 布尔 | 必须是 JSON 布尔值，传数字会返回 10163 类型校验错误 | 静态分析及前期测试 |
| `vad_timeout`、`vad_speech_tail` | 数字 | 官方客户端会和 `eos` 设成同一个值 | 静态分析；单独设置 `vad_speech_tail` 实测无效果 |

### eos：停顿多久算说完

实测同一段语音（说话约在 1.8 秒处结束），服务端发出最终结果的时间：

| `eos` | 最终结果到达 |
|---|---|
| 不传 | 约 3.5 秒 |
| 4000 | 约 5.8 秒 |
| 6000 | 约 7.8 秒 |

也就是服务端在说话停止后等待 `eos` 毫秒，然后给出最终结果并结束这次识别；不传时默认约 1.8 秒。`vad_enable` 是 `true` 还是 `false`，对这个行为没有影响。

XVoice 的“自动结束”设置就是把 `eos` 设成用户选的时长，服务端结束后客户端也跟着结束听写。

## 模型选择

这是最容易出错的部分，有两条实测结论：

1. **服务端不认识的值不会报错，而是悄悄用默认的普通话模型识别。** 所以“请求成功”不能说明参数有效。
2. **只要请求里带了 `language` 或 `accent`，`ent` 就被忽略。** 例如同时发 `language=zh_cn`、`accent=mandarin`、`ent=tibetan16k`，得到的是普通话模型的结果；只发 `ent=tibetan16k` 才是藏语模型。

验证方法：用同一段英文语音请求各个模型，和默认模型的输出对比。输出不同，说明模型确实切换了。例如同一句英文，藏语模型输出藏文，日语模型输出片假名，默认模型输出英文原文。

### 实测生效的取值

只发 `ent`：

| 语言 | `ent` |
|---|---|
| 普通话（默认） | `sms16k` |
| 粤语 | `cantonese-ed16k` |
| 英语 | `sms-en16k` |
| 四川话 | `zhongchuan` |
| 河南话、东北话、天津话 | `henanese16k`、`dongbeiese16k`、`tianjinese16k` |
| 湖南话（长沙）、山东话（济南）、武汉话 | `changshanese16k`、`shandongnese16k`、`wuhanese16k` |
| 合肥话、江西话（南昌）、河北话 | `hefeinese16k`、`nanchangnese16k`、`hebeinese16k` |
| 闽南语、客家语、贵州话 | `minnanese16k`、`hakkanese16k`、`guizhounese16k` |
| 甘肃话、宁夏话、国语（台湾） | `gansunese16k`、`ningxianese16k`、`taiwanese` |
| 云南话（昆明）、山西话（太原）、陕西话（西安） | `yunnanese16k`、`taiyuanese16k`、`shanxinese16k` |
| 皖北话、上海话、苏州话、南京话 | `wanbeinese`、`shanghainese16k`、`suzhounese`、`nankinese16k` |
| 藏语、维吾尔语、朝鲜语 | `tibetan16k`、`uyghur16k`、`korean` |

发 `language=<值>`、`domain=iat`、`accent=mandarin`，不发 `ent`：

| 语言 | `language` |
|---|---|
| 日语 | `ja_jp` |
| 韩语 | `ko_kr` |

各方言模型对英文语音的输出彼此相同，只是和默认模型不同。所以能确认的是“参数让服务端换了模型”，不能确认每种方言各有一个独立模型，方言的识别效果也没有验证。

### 实测不生效的取值

下面这些值出现在官方客户端的语言表里，但在这个接口上不管怎么搭配参数，结果都和默认模型一样：

- 外语：`ru-ru`、`th_TH`、`vi_VN`、`fr_fr`、`de_DE`、`es_es`、`pt_PT`、`it_IT`、`ar_il`
- 其他：`yinese`（彝语）、`zhuangnese`（壮语）、`chaoshannese`、`hangzhounese`、`jinhuaese`、`xfime-conformer`

官方客户端可能通过别的参数或别的接口使用它们，没有继续研究。

### 官方客户端的拼法

官方客户端内部用数字编号表示语言，按编号范围决定参数怎么拼（静态分析）：

| 编号 | 拼出的参数 |
|---|---|
| 小于 104，以及 201、234、519 | `ent=<值>` |
| 104 到 200（201 除外） | `language=zh_cn,accent=<值>,domain=iat` |
| 202 及以上（234、519 除外） | `language=<值>,domain=iat,accent=mandarin` |

哪个语言对应哪个编号没有恢复出来。`taiwanese`、`wanbeinese`、`suzhounese` 用 `ent` 和用 `accent` 两种拼法实测都生效，XVoice 统一用 `ent`。

## 服务端返回的消息

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "status": 1,
    "result": {
      "sn": 13,
      "pgs": "rpl",
      "rg": [1, 12],
      "rst": "rlt",
      "ws": [ { "cw": [ { "w": "早上好，今天的日程安排是什么" } ] } ]
    }
  }
}
```

| 字段 | 含义 |
|---|---|
| `code` | 0 为成功，其他为错误 |
| `data.status` | 2 表示这次识别结束，之后服务端不再发送结果 |
| `sn` | 这条结果的序号，从 1 递增 |
| `pgs` | `apd`：追加一段；`rpl`：替换序号在 `rg` 范围内的那些段 |
| `rg` | 被替换的序号范围，闭区间 |
| `rst` | `pgs`：临时结果，之后还会变；`rlt`：这一句已经定稿 |
| `ws[].cw[0].w` | 这一段的文字 |

### 拼出完整文字

按序号保存每一段。收到 `rpl` 时先删掉序号落在 `rg` 范围内的段，再存入新的一段；最后把所有段按序号从小到大拼起来。

一段真实的消息流（说的是“早上好，今天的日程安排是什么？帮我订一张明天去北京的机票。”）：

| 时间 | sn | pgs | rg | rst | status | 这一段的文字 |
|---|---|---|---|---|---|---|
| 1.0 s | 1 | apd | | pgs | 0 | 早 |
| 1.3 s | 2 | rpl | 1–1 | pgs | 1 | 早上 |
| … | | | | | | |
| 4.2 s | 12 | rpl | 1–11 | pgs | 1 | 早上好今天的日程安排是什 |
| 5.0 s | 13 | rpl | 1–12 | rlt | 1 | 早上好，今天的日程安排是什么 |
| 5.2 s | 14 | apd | | pgs | 1 | 帮 |
| … | | | | | | |
| 8.1 s | 26 | rpl | 14–25 | rlt | 1 | ？帮我订一张明天去北京的机票 |
| 8.1 s | 27 | apd | | rlt | 2 | 。 |

从中可以看出（实测）：

- 临时结果没有标点。一句话定稿（`rlt`）时才加上句中的标点。
- **一句话末尾的标点是延后给出的**：第一句的问号出现在第二句定稿的那一段开头，最后一句的句号要等识别结束才给。所以中途直接断开连接，最后一句会没有句末标点。
- 一句话定稿后，它之前的内容不会再变。XVoice 据此区分“已定稿”和“未定稿”的文字。
- 英语模型的输出以一个空格开头。

## 连接的生命周期

实测，使用 `ent=sms16k`、不传 `eos`：

- **说话后停顿**：说话停止约 1.8 秒后，服务端返回最终结果（`status` 为 2）并结束这次识别。之后的语音需要新开一个连接。
- **一直没有说话**：约 10 秒后，服务端返回一条没有文字的最终结果（`code` 为 0，`status` 为 2）并结束。
- **客户端主动结束**：发送 `status` 为 2 的空帧，服务端返回最终结果后结束。

因此一次连续的听写会跨越多个连接。XVoice 的做法：

- 麦克风一直开着，录到的声音先进队列。上一个连接结束后，下一帧音频到来时立刻新开连接，排队的声音随后补发。
- 把每个连接识别出的文字接在前面的文字之后。
- 每 2 秒发一次 ping。连续 8 秒没有收到任何帧（包括 pong）才认为连接已断。
- 连接在 3 秒内建立不起来，停止听写。
- 连续 3 个连接都没有识别出文字（约 30 秒没说话），停止听写。
- 单个连接发满 50 秒的音频后主动结束并换新连接。服务端对单个连接的时长上限没有实测过，这个数字是保守取值。

## 错误码

| 错误码 | 情况 | 来源 |
|---|---|---|
| 10118 | 服务端结束了一个没有声音的连接。XVoice 把它当作普通的连接结束处理 | 沿用自项目最初的实现，没有亲自观察到；实测中服务端返回的是上面说的空的最终结果 |
| 10163 | 参数类型不对，例如 `vad_enable` 传了数字 | 前期测试 |

其他非零的 `code`，XVoice 直接报错并停止，不重连。

## 没有研究的部分

- 官方客户端的随声译（边说边翻译）：语言表里有 `zh_cn,from=zh,to=en` 这样的值和 `xfime-cbm` 模型。
- 服务端通过响应头 `X-Protocol: binary` 切换到二进制音频帧的分支。（静态分析）
- Opus、Speex 编码的音频。
- 个性化词库和热词，官方客户端通过独立的 HTTP 接口同步。
- 离线识别引擎。
