#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PROMO_DIR="$ROOT_DIR/promo"
OUT="$PROMO_DIR/easy_terminal_promo.mp4"
AUDIO_RAW="$PROMO_DIR/voiceover.wav"
AUDIO="$PROMO_DIR/voiceover_fast.wav"
SCRIPT="$PROMO_DIR/voiceover.txt"
FONT="/System/Library/Fonts/Hiragino Sans GB.ttc"
GITHUB_URL="${GITHUB_URL:-https://github.com/elevenlj/easy_terminal}"
GITHUB_DISPLAY="${GITHUB_URL#https://}"
GITHUB_DISPLAY="${GITHUB_DISPLAY#http://}"
TTS_PY="/Users/eleven/develop/model/.venv-tts/bin/python"

if [[ ! -f "$FONT" ]]; then
  FONT="/System/Library/Fonts/STHeiti Medium.ttc"
fi

mkdir -p "$PROMO_DIR"

if [[ ! -f "$AUDIO_RAW" || "${FORCE_TTS:-0}" == "1" ]]; then
  "$TTS_PY" "$PROMO_DIR/generate_qwen3_voiceover.py" \
    --script "$SCRIPT" \
    --output "$AUDIO_RAW" \
    --speaker "${QWEN3_TTS_SPEAKER:-Uncle_Fu}"
fi

ffmpeg -y -i "$AUDIO_RAW" -filter:a "atempo=1.55" "$AUDIO"

python3 "$PROMO_DIR/generate_promo_slides.py" \
  --out-dir "$PROMO_DIR/slides" \
  --github "$GITHUB_DISPLAY"

ffmpeg -y \
  -f concat -safe 0 -i "$PROMO_DIR/slides/slides.txt" \
  -i "$AUDIO" \
  -vf "format=yuv420p" \
  -c:v libx264 -preset medium -crf 18 -r 30 \
  -c:a aac -b:a 192k \
  -shortest "$OUT"

ffprobe -v error -show_entries format=duration,size -of default=noprint_wrappers=1 "$OUT"
exit 0

ffmpeg -y \
  -f lavfi -i "color=c=0B1020:s=1080x1920:r=30:d=78" \
  -i "$AUDIO" \
  -filter_complex "
    [0:v]
    drawbox=x=0:y=0:w=1080:h=1920:color=0B1020@1:t=fill,
    drawbox=x=58:y=120:w=964:h=1680:color=101827@0.78:t=fill,
    drawbox=x=58:y=120:w=964:h=1680:color=7DD3FC@0.32:t=3,
    drawbox=x=88:y=157:w=18:h=18:color=FB7185@1:t=fill,
    drawbox=x=122:y=157:w=18:h=18:color=FBBF24@1:t=fill,
    drawbox=x=156:y=157:w=18:h=18:color=34D399@1:t=fill,
    drawtext=fontfile='$FONT':text='Easy Terminal':x=88:y=214:fontsize=54:fontcolor=F8FAFC,
    drawtext=fontfile='$FONT':text='$GITHUB_DISPLAY':x=88:y=284:fontsize=28:fontcolor=93C5FD,
    drawtext=fontfile='$FONT':text='比 OpenClaw、Hermes 更接地气的 Agent':x=(w-text_w)/2:y=560:fontsize=62:fontcolor=FFFFFF:enable='between(t,0,6)',
    drawtext=fontfile='$FONT':text='不是再做一个新的智能体':x=(w-text_w)/2:y=730:fontsize=44:fontcolor=CBD5E1:enable='between(t,1.0,6)',
    drawtext=fontfile='$FONT':text='而是先控制终端':x=(w-text_w)/2:y=805:fontsize=58:fontcolor=7DD3FC:enable='between(t,2.0,6)',
    drawtext=fontfile='$FONT':text='真正工作的 Agent，本来就跑在终端里':x=(w-text_w)/2:y=560:fontsize=50:fontcolor=FFFFFF:enable='between(t,6,14)',
    drawbox=x=166:y=720:w=748:h=292:color=020617@0.92:t=fill:enable='between(t,6,14)',
    drawtext=fontfile='$FONT':text='\\$ opencode':x=206:y=770:fontsize=38:fontcolor=34D399:enable='between(t,6.5,14)',
    drawtext=fontfile='$FONT':text='\\$ codex':x=206:y=835:fontsize=38:fontcolor=7DD3FC:enable='between(t,7.4,14)',
    drawtext=fontfile='$FONT':text='\\$ claude-code':x=206:y=900:fontsize=38:fontcolor=FBBF24:enable='between(t,8.3,14)',
    drawtext=fontfile='$FONT':text='\\$ gemini':x=206:y=965:fontsize=38:fontcolor=FB7185:enable='between(t,9.2,14)',
    drawtext=fontfile='$FONT':text='终端能跑什么，Easy Terminal 就能远程使用什么':x=(w-text_w)/2:y=1120:fontsize=38:fontcolor=CBD5E1:enable='between(t,10,14)',
    drawtext=fontfile='$FONT':text='云端 Agent':x=160:y=520:fontsize=46:fontcolor=CBD5E1:enable='between(t,14,24)',
    drawtext=fontfile='$FONT':text='Easy Terminal':x=603:y=520:fontsize=46:fontcolor=FFFFFF:enable='between(t,14,24)',
    drawbox=x=120:y=610:w=380:h=510:color=1E293B@0.95:t=fill:enable='between(t,14,24)',
    drawbox=x=580:y=610:w=380:h=510:color=0F766E@0.70:t=fill:enable='between(t,14,24)',
    drawtext=fontfile='$FONT':text='依赖 API key':x=165:y=685:fontsize=34:fontcolor=FCA5A5:enable='between(t,14.5,24)',
    drawtext=fontfile='$FONT':text='单一平台能力':x=165:y=765:fontsize=34:fontcolor=FCA5A5:enable='between(t,15.2,24)',
    drawtext=fontfile='$FONT':text='任务结束后通知':x=165:y=845:fontsize=34:fontcolor=FCA5A5:enable='between(t,15.9,24)',
    drawtext=fontfile='$FONT':text='本地或服务器执行':x=625:y=685:fontsize=34:fontcolor=CCFBF1:enable='between(t,16.6,24)',
    drawtext=fontfile='$FONT':text='任意 CLI Agent':x=625:y=765:fontsize=34:fontcolor=CCFBF1:enable='between(t,17.3,24)',
    drawtext=fontfile='$FONT':text='实时可见可接管':x=625:y=845:fontsize=34:fontcolor=CCFBF1:enable='between(t,18,24)',
    drawtext=fontfile='$FONT':text='不是黑盒执行，而是掌控自己的工作环境':x=(w-text_w)/2:y=1240:fontsize=40:fontcolor=FFFFFF:enable='between(t,19,24)',
    drawtext=fontfile='$FONT':text='成本更低':x=(w-text_w)/2:y=465:fontsize=64:fontcolor=FFFFFF:enable='between(t,24,33)',
    drawtext=fontfile='$FONT':text='免费 / 开源 / 本地 CLI Agent 都能接入':x=(w-text_w)/2:y=570:fontsize=40:fontcolor=BAE6FD:enable='between(t,24.8,33)',
    drawtext=fontfile='$FONT':text='不被某一个云端平台绑定':x=(w-text_w)/2:y=650:fontsize=40:fontcolor=CBD5E1:enable='between(t,25.6,33)',
    drawbox=x=170:y=820:w=740:h=170:color=020617@0.88:t=fill:enable='between(t,26.4,33)',
    drawtext=fontfile='$FONT':text='OpenCode  /  Codex CLI  /  Claude Code':x=(w-text_w)/2:y=875:fontsize=34:fontcolor=F8FAFC:enable='between(t,26.4,33)',
    drawtext=fontfile='$FONT':text='本地模型  /  内部脚本  /  自动化工具':x=(w-text_w)/2:y=935:fontsize=34:fontcolor=F8FAFC:enable='between(t,27.2,33)',
    drawtext=fontfile='$FONT':text='能力更强':x=(w-text_w)/2:y=420:fontsize=64:fontcolor=FFFFFF:enable='between(t,33,44)',
    drawtext=fontfile='$FONT':text='控制终端，就连接真实开发环境':x=(w-text_w)/2:y=525:fontsize=42:fontcolor=BAE6FD:enable='between(t,33.5,44)',
    drawbox=x=150:y=690:w=780:h=420:color=020617@0.92:t=fill:enable='between(t,34,44)',
    drawtext=fontfile='$FONT':text='启动服务':x=220:y=760:fontsize=38:fontcolor=34D399:enable='between(t,34.6,44)',
    drawtext=fontfile='$FONT':text='看日志':x=570:y=760:fontsize=38:fontcolor=7DD3FC:enable='between(t,35.2,44)',
    drawtext=fontfile='$FONT':text='跑测试':x=220:y=860:fontsize=38:fontcolor=FBBF24:enable='between(t,35.8,44)',
    drawtext=fontfile='$FONT':text='改代码':x=570:y=860:fontsize=38:fontcolor=FB7185:enable='between(t,36.4,44)',
    drawtext=fontfile='$FONT':text='执行脚本':x=220:y=960:fontsize=38:fontcolor=A78BFA:enable='between(t,37,44)',
    drawtext=fontfile='$FONT':text='操作 Git':x=570:y=960:fontsize=38:fontcolor=F8FAFC:enable='between(t,37.6,44)',
    drawtext=fontfile='$FONT':text='终端能做什么，Agent 就能做什么':x=(w-text_w)/2:y=1210:fontsize=42:fontcolor=FFFFFF:enable='between(t,38.4,44)',
    drawtext=fontfile='$FONT':text='飞书里实时看，随时接管':x=(w-text_w)/2:y=420:fontsize=56:fontcolor=FFFFFF:enable='between(t,44,55)',
    drawbox=x=205:y=590:w=670:h=650:color=0F172A@0.96:t=fill:enable='between(t,44,55)',
    drawbox=x=250:y=680:w=465:h=82:color=1E293B@1:t=fill:enable='between(t,44.8,55)',
    drawtext=fontfile='$FONT':text='Agent 正在跑测试...':x=280:y=702:fontsize=32:fontcolor=E2E8F0:enable='between(t,44.8,55)',
    drawbox=x=365:y=805:w=435:h=82:color=0F766E@1:t=fill:enable='between(t,46,55)',
    drawtext=fontfile='$FONT':text='先别改数据库':x=405:y=827:fontsize=32:fontcolor=FFFFFF:enable='between(t,46,55)',
    drawbox=x=250:y=930:w=520:h=82:color=1E293B@1:t=fill:enable='between(t,47.2,55)',
    drawtext=fontfile='$FONT':text='收到，切换为只读检查':x=280:y=952:fontsize=32:fontcolor=E2E8F0:enable='between(t,47.2,55)',
    drawtext=fontfile='$FONT':text='不是等结束后通知，而是执行过程全程可见':x=(w-text_w)/2:y=1340:fontsize=38:fontcolor=BAE6FD:enable='between(t,48.2,55)',
    drawtext=fontfile='$FONT':text='多会话并行':x=(w-text_w)/2:y=410:fontsize=60:fontcolor=FFFFFF:enable='between(t,55,63)',
    drawbox=x=132:y=610:w=360:h=220:color=020617@0.95:t=fill:enable='between(t,55,63)',
    drawbox=x=588:y=610:w=360:h=220:color=020617@0.95:t=fill:enable='between(t,55,63)',
    drawbox=x=132:y=890:w=360:h=220:color=020617@0.95:t=fill:enable='between(t,55,63)',
    drawbox=x=588:y=890:w=360:h=220:color=020617@0.95:t=fill:enable='between(t,55,63)',
    drawtext=fontfile='$FONT':text='前端服务':x=215:y=695:fontsize=36:fontcolor=34D399:enable='between(t,55.5,63)',
    drawtext=fontfile='$FONT':text='后端日志':x=672:y=695:fontsize=36:fontcolor=7DD3FC:enable='between(t,56,63)',
    drawtext=fontfile='$FONT':text='Agent 修复':x=215:y=975:fontsize=36:fontcolor=FBBF24:enable='between(t,56.5,63)',
    drawtext=fontfile='$FONT':text='自动测试':x=672:y=975:fontsize=36:fontcolor=FB7185:enable='between(t,57,63)',
    drawtext=fontfile='$FONT':text='真实开发不是单线程，Easy Terminal 也不是':x=(w-text_w)/2:y=1250:fontsize=38:fontcolor=CBD5E1:enable='between(t,58,63)',
    drawtext=fontfile='$FONT':text='Easy Terminal':x=(w-text_w)/2:y=530:fontsize=72:fontcolor=FFFFFF:enable='between(t,63,70)',
    drawtext=fontfile='$FONT':text='不是又一个 Agent':x=(w-text_w)/2:y=660:fontsize=46:fontcolor=CBD5E1:enable='between(t,63.5,70)',
    drawtext=fontfile='$FONT':text='它是所有终端 Agent 的入口':x=(w-text_w)/2:y=745:fontsize=52:fontcolor=7DD3FC:enable='between(t,64.2,70)',
    drawtext=fontfile='$FONT':text='控制终端 = 控制所有能在终端里运行的智能体':x=(w-text_w)/2:y=940:fontsize=40:fontcolor=FFFFFF:enable='between(t,65.2,70)',
    drawtext=fontfile='$FONT':text='$GITHUB_DISPLAY':x=(w-text_w)/2:y=1160:fontsize=34:fontcolor=93C5FD:enable='between(t,66,78)',
    format=yuv420p[v]
  " \
  -map "[v]" -map 1:a \
  -c:v libx264 -preset medium -crf 18 \
  -c:a aac -b:a 192k \
  -shortest "$OUT"

ffprobe -v error -show_entries format=duration,size -of default=noprint_wrappers=1 "$OUT"
