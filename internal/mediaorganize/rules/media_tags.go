package rules

import (
	"fmt"
	"regexp"
	"strings"
)

var qualityTokenRe = regexp.MustCompile(`(?i)(?:4320[pP]|2160[pP]|1080[pP]|720[pP]|480[pP]|4[Kk]|2[Kk]|8[Kk]|UHD|FHD|FullHD|WEB[-. ]?DL|WEB[-. ]?Rip|BluRay|BDRip|BDMV|BD25|BD50|HDTV|HDTVrip|DVDRip|DVD[-. ]?9|DVD[-. ]?5|REMUX|Repack|Proper|Extended|Director'?s[. ]Cut|Theatrical|Uncut|HDR10\+?|HDR|Dolby[. ]Vision|DoVi|SDR|HLG|10[. ]?bit|8[. ]?bit|H\.?264|H\.?265|HEVC|AVC|x264|x265|VP9|AV1|DTS[-.]?HD[. ]?MA|DTS[-.]?HD[. ]?HRA|DTS[-.]?HD|DTS[-.]?X|DTS|DDP|DD\+|DD|AC3|EAC3|TrueHD|Atmos|FLAC|AAC|OPUS|MP3|PCM|\d{2,3}(?:\.\d+)?fps|MultiAudio|Multi[. ]?Lang)`)

var (
	combinedAACChannelsRe = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9])AAC(\d\.\d)(?:$|[^A-Za-z0-9])`)
	combinedPCMChannelsRe = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9])PCM(\d\.\d)(?:$|[^A-Za-z0-9])`)
	combinedDDChannelsRe  = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9])DD\+?(\d\.\d)(?:$|[^A-Za-z0-9])`)
	combinedAC3ChannelsRe = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9])AC3[-.]?(\d\.\d)(?:$|[^A-Za-z0-9])`)
	dtsXPatternRe         = regexp.MustCompile(`(?i)DTS[-.]?X`)
	dtsHDMAPatternRe      = regexp.MustCompile(`(?i)DTS[-.]?HD[-.]?MA`)
	dtsHDHRAPatternRe     = regexp.MustCompile(`(?i)DTS[-.]?HD[-.]?HRA`)
	dolbyTrueHDPatternRe  = regexp.MustCompile(`(?i)(?:Dolby[-.]?)?TrueHD`)
	channelLayoutRe       = regexp.MustCompile(`(?:^|[^0-9])([157]\.[01]|2\.0|2\.1|6\.1|1\.0)(?:$|[^0-9])`)
	frameRateTokenRe      = regexp.MustCompile(`(?i)(?:^|[^0-9])(\d{2,3}(?:\.\d+)?)\s*fps(?:$|[^0-9])`)
	bracketInnerRe        = regexp.MustCompile(`\[([^\]]+)\]|【([^】]+)】`)
)

var validChannelLayouts = map[string]struct{}{
	"1.0": {}, "2.0": {}, "2.1": {}, "4.0": {}, "5.0": {}, "5.1": {}, "6.1": {}, "7.0": {}, "7.1": {},
}

func EnrichMediaTagsFromFilename(name string, m map[string]any) {
	if m == nil {
		return
	}
	stem, _ := splitStemExt(strings.TrimSpace(name))
	if stem == "" {
		return
	}
	scanned := scanMediaTagsFromStem(stem)
	mergeScannedScreenSize(m, scanned.screenSize)
	mergeScannedString(m, "frame_rate", scanned.frameRate)
	mergeScannedString(m, "video_codec", scanned.videoCodec)
	mergeScannedAudioCodec(m, scanned.audioCodec)
	mergeScannedString(m, "audio_channels", scanned.audioChannels)
}

func mergeScannedScreenSize(m map[string]any, scanned string) {
	if scanned == "" {
		return
	}
	if mediaTagEmpty(m, "screen_size") {
		m["screen_size"] = scanned
		return
	}
	old := strings.ToLower(fmt.Sprint(m["screen_size"]))
	if screenSizeRank(scanned) > screenSizeRank(old) {
		m["screen_size"] = scanned
	}
}

func mergeScannedString(m map[string]any, key, scanned string) {
	if scanned == "" {
		return
	}
	if mediaTagEmpty(m, key) {
		m[key] = scanned
	}
}

func mergeScannedAudioCodec(m map[string]any, scanned string) {
	if scanned == "" {
		return
	}
	if mediaTagEmpty(m, "audio_codec") {
		m["audio_codec"] = scanned
		return
	}
	old := fmt.Sprint(m["audio_codec"])
	if audioCodecRank(scanned) > audioCodecRank(old) {
		m["audio_codec"] = scanned
	}
}

func audioCodecRank(codec string) int {
	_, rank := classifyAudioCodecToken(strings.TrimSpace(codec))
	if rank > 0 {
		return rank
	}
	switch strings.ToUpper(strings.TrimSpace(codec)) {
	case "DTS:X", "DTS-X":
		return 75
	case "DTS-HD MA":
		return 90
	case "DTS-HD HRA":
		return 85
	case "DTS-HD":
		return 80
	case "DTS":
		return 70
	case "TRUEHD":
		return 100
	case "DDP":
		return 60
	case "DD":
		return 55
	case "FLAC":
		return 50
	case "AAC":
		return 40
	case "PCM":
		return 30
	default:
		return 10
	}
}

func enrichParsedMediaTags(name string, p ParsedMedia) ParsedMedia {
	m := p.ToMap()
	EnrichMediaTagsFromFilename(name, m)
	return parsedFromMap(m)
}

func mediaTagEmpty(m map[string]any, key string) bool {
	if m == nil {
		return true
	}
	v, ok := m[key]
	if !ok || v == nil {
		return true
	}
	return strings.TrimSpace(fmt.Sprint(v)) == ""
}

type mediaTagScanResult struct {
	screenSize    string
	frameRate     string
	videoCodec    string
	audioCodec    string
	audioChannels string
}

func scanMediaTagsFromStem(stem string) mediaTagScanResult {
	out := mediaTagScanResult{}
	scanText := expandStemForTagScan(stem)

	applyCombinedPatterns(scanText, &out)

	screenRank := -1
	audioRank := -1
	for _, token := range qualityTokenRe.FindAllString(scanText, -1) {
		field, value, rank := classifyQualityToken(token)
		if field == "" || value == "" {
			continue
		}
		switch field {
		case "screen_size":
			if rank > screenRank {
				screenRank = rank
				out.screenSize = value
			}
		case "frame_rate":
			if out.frameRate == "" {
				out.frameRate = value
			}
		case "video_codec":
			if out.videoCodec == "" {
				out.videoCodec = value
			}
		case "audio_codec":
			if rank > audioRank {
				audioRank = rank
				out.audioCodec = value
			}
		case "audio_channels":
			if out.audioChannels == "" {
				out.audioChannels = value
			}
		}
	}
	return out
}

func expandStemForTagScan(stem string) string {
	var b strings.Builder
	b.WriteString(stem)
	b.WriteByte(' ')
	for _, inner := range bracketInners(stem) {
		b.WriteString(strings.TrimSpace(inner))
		b.WriteByte(' ')
	}
	return b.String()
}

func applyCombinedPatterns(text string, out *mediaTagScanResult) {
	if m := combinedAACChannelsRe.FindStringSubmatch(text); len(m) >= 2 {
		out.audioCodec = "AAC"
		out.audioChannels = m[1]
	}
	if m := combinedPCMChannelsRe.FindStringSubmatch(text); len(m) >= 2 {
		out.audioCodec = "PCM"
		out.audioChannels = m[1]
	}
	if m := combinedDDChannelsRe.FindStringSubmatch(text); len(m) >= 2 {
		if out.audioCodec == "" {
			out.audioCodec = "DDP"
		}
		if out.audioChannels == "" {
			out.audioChannels = m[1]
		}
	}
	if m := combinedAC3ChannelsRe.FindStringSubmatch(text); len(m) >= 2 {
		if out.audioCodec == "" {
			out.audioCodec = "DD"
		}
		if out.audioChannels == "" {
			out.audioChannels = m[1]
		}
	}
	if dtsXPatternRe.MatchString(text) {
		out.audioCodec = "DTS:X"
	} else if dtsHDMAPatternRe.MatchString(text) && out.audioCodec == "" {
		out.audioCodec = "DTS-HD MA"
	}
	if dtsHDHRAPatternRe.MatchString(text) && out.audioCodec == "" {
		out.audioCodec = "DTS-HD HRA"
	}
	if dolbyTrueHDPatternRe.MatchString(text) && out.audioCodec == "" {
		out.audioCodec = "TrueHD"
	}
	if out.audioChannels == "" {
		if m := channelLayoutRe.FindStringSubmatch(text); len(m) >= 2 {
			out.audioChannels = m[1]
		}
	}
}

func classifyQualityToken(raw string) (field, value string, rank int) {
	token := strings.TrimSpace(raw)
	if token == "" {
		return "", "", 0
	}

	if m := frameRateTokenRe.FindStringSubmatch(" " + token + " "); len(m) >= 2 {
		return "frame_rate", normalizeFrameRate(m[1]), 0
	}
	if strings.HasSuffix(strings.ToLower(token), "fps") {
		return "frame_rate", normalizeFrameRate(strings.TrimSuffix(strings.ToLower(token), "fps")), 0
	}

	if size, r := classifyScreenSizeToken(token); size != "" {
		return "screen_size", size, r
	}
	if ch := classifyChannelToken(token); ch != "" {
		return "audio_channels", ch, 0
	}
	if vc := classifyVideoCodecToken(token); vc != "" {
		return "video_codec", vc, 0
	}
	if ac, pr := classifyAudioCodecToken(token); ac != "" {
		return "audio_codec", ac, pr
	}
	return "", "", 0
}

func classifyScreenSizeToken(token string) (string, int) {
	switch strings.ToUpper(strings.TrimSpace(token)) {
	case "8K":
		return "4320p", 5
	case "4K", "UHD":
		return "2160p", 4
	case "2K":
		return "1080p", 3
	case "FHD", "FULLHD":
		return "1080p", 3
	}
	m := regexp.MustCompile(`(?i)^(4320|2160|1080|720|480)[pP]$`).FindStringSubmatch(token)
	if len(m) >= 2 {
		size := strings.ToLower(m[1]) + "p"
		return size, screenSizeRank(size)
	}
	return "", 0
}

func classifyChannelToken(token string) string {
	if m := regexp.MustCompile(`^(\d\.\d)$`).FindStringSubmatch(strings.TrimSpace(token)); len(m) >= 2 {
		if _, ok := validChannelLayouts[m[1]]; ok {
			return m[1]
		}
	}
	return ""
}

func classifyVideoCodecToken(token string) string {
	switch strings.ToLower(strings.TrimSpace(token)) {
	case "h.264", "h264", "x264", "avc":
		return "H.264"
	case "h.265", "h265", "x265", "hevc":
		return "H.265"
	case "av1":
		return "AV1"
	case "vp9":
		return "VP9"
	default:
		return ""
	}
}

func classifyAudioCodecToken(token string) (string, int) {
	norm := strings.ToLower(strings.TrimSpace(token))
	norm = regexp.MustCompile(`[\s._-]+`).ReplaceAllString(norm, " ")
	switch {
	case strings.Contains(norm, "dts-x"), norm == "dts:x":
		return "DTS:X", 75
	case strings.Contains(norm, "dts-hd ma"), norm == "dtshdma":
		return "DTS-HD MA", 90
	case strings.Contains(norm, "dts-hd hra"):
		return "DTS-HD HRA", 85
	case strings.Contains(norm, "dts-hd"):
		return "DTS-HD", 80
	case norm == "dts":
		return "DTS", 70
	case norm == "ddp", norm == "dd+", strings.Contains(norm, "eac3"):
		return "DDP", 60
	case norm == "dd", norm == "ac3":
		return "DD", 55
	case strings.Contains(norm, "truehd"):
		return "TrueHD", 100
	case norm == "flac":
		return "FLAC", 50
	case norm == "aac":
		return "AAC", 40
	case norm == "pcm":
		return "PCM", 30
	case norm == "opus":
		return "Opus", 25
	case norm == "mp3":
		return "MP3", 20
	case norm == "atmos", strings.Contains(norm, "dolby atmos"):
		return "", 0
	default:
		return "", 0
	}
}

func bracketInners(stem string) []string {
	out := make([]string, 0)
	for _, m := range bracketInnerRe.FindAllStringSubmatch(stem, -1) {
		if len(m) >= 2 && strings.TrimSpace(m[1]) != "" {
			out = append(out, m[1])
		}
		if len(m) >= 3 && strings.TrimSpace(m[2]) != "" {
			out = append(out, m[2])
		}
	}
	return out
}

func screenSizeRank(token string) int {
	switch strings.ToLower(strings.TrimSpace(token)) {
	case "4320p", "8k":
		return 5
	case "2160p", "4k", "uhd":
		return 4
	case "1080p", "fhd", "2k":
		return 3
	case "720p":
		return 2
	case "480p":
		return 1
	default:
		return 0
	}
}
