#!/bin/bash
set -euo pipefail

export HOME=/home/edub

pkgs=()

# --- Conditionally install external tools based on engine env vars ---

if [ -n "${OCR_ENGINE:-}" ]; then
  case "$OCR_ENGINE" in
    ocrmypdf)
      pkgs+=(ocrmypdf tesseract-ocr unpaper pngquant)
      if [ -n "${OCR_LANGUAGES:-}" ]; then
        IFS=',' read -ra langs <<< "$OCR_LANGUAGES"
        for lang in "${langs[@]}"; do
          pkgs+=("tesseract-ocr-${lang}")
        done
      fi
      ;;
  esac
fi

if [ -n "${PDF_OPTIMIZER_ENGINE:-}" ]; then
  case "$PDF_OPTIMIZER_ENGINE" in
    gs) pkgs+=(ghostscript) ;;
  esac
fi

if [ -n "${TEXT_EXTRACTOR_ENGINE:-}" ]; then
  case "$TEXT_EXTRACTOR_ENGINE" in
    pdftotext) pkgs+=(poppler-utils) ;;
  esac
fi

if [ ${#pkgs[@]} -gt 0 ]; then
  install_needed=()
  for pkg in "${pkgs[@]}"; do
    # Map package name to binary name for `command -v` check
    bin=""
    case "$pkg" in
      ocrmypdf)                    bin="ocrmypdf" ;;
      tesseract-ocr)               bin="tesseract" ;;
      unpaper)                     bin="unpaper" ;;
      pngquant)                    bin="pngquant" ;;
      ghostscript)                 bin="gs" ;;
      poppler-utils)               bin="pdftotext" ;;
      tesseract-ocr-*)
        lang_code="${pkg#tesseract-ocr-}"
        found=0
        for dir in /usr/share/tesseract-ocr/*/tessdata/; do
          [ -f "${dir}${lang_code}.traineddata" ] && found=1 && break
        done
        [ "$found" = "1" ] && bin="true" || bin=""
        ;; # check by traineddata file presence
    esac
    if [ -n "$bin" ] && command -v "$bin" &>/dev/null; then
      : # already installed
    else
      install_needed+=("$pkg")
    fi
  done

  if [ ${#install_needed[@]} -gt 0 ]; then
    apt-get update
    apt-get install -y --no-install-recommends "${install_needed[@]}"
    rm -rf /var/lib/apt/lists/*
  fi
fi

# --- First-boot setup ---

if [ ! -f "$HOME/.config/edub-kushim/config.yaml" ]; then
  mkdir -p "$HOME/.config/edub-kushim"
  if [ -z "${OCR_LANGUAGES:-}" ]; then
    echo "ERROR: OCR_LANGUAGES is required for first-time setup." >&2
    echo "Set it in your docker-compose.yml environment block, e.g.:" >&2
    echo "  OCR_LANGUAGES=eng" >&2
    echo "  OCR_LANGUAGES=eng,spa" >&2
    exit 1
  fi

  args=(setup --cli --languages "$OCR_LANGUAGES")

  if [ -n "${DB_DSN:-}" ]; then
    args+=(--db-dsn "$DB_DSN")
  fi

  if [ -n "${ADMIN_USER:-}" ]; then
    args+=(--admin-user "$ADMIN_USER")
  fi

  if [ -n "${ADMIN_PASSWORD:-}" ]; then
    args+=(--admin-password "$ADMIN_PASSWORD")
  fi

  if [ -n "${OCR_ENGINE:-}" ]; then
    args+=(--consumer-ocr-engine "$OCR_ENGINE")
  fi

  if [ -n "${PDF_OPTIMIZER_ENGINE:-}" ]; then
    args+=(--consumer-pdfoptimizer-engine "$PDF_OPTIMIZER_ENGINE")
  fi

  if [ -n "${TEXT_EXTRACTOR_ENGINE:-}" ]; then
    args+=(--consumer-textextractor-engine "$TEXT_EXTRACTOR_ENGINE")
  fi

  if [ -n "${INBOX_PATH:-}" ]; then
    args+=(--inbox-path "$INBOX_PATH")
  fi

  kushim "${args[@]}"
fi

chown -R edub:edub /home/edub
exec gosu edub "$@"
