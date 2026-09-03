from pathlib import Path

p = Path("telegram.go")
s = p.read_text()
old = "return string([]rune{rune(0x1F1E6 + cc[0] - 'A'), rune(0x1F1E6 + cc[1] - 'A')})"
new = "return string([]rune{rune(0x1F1E6) + rune(cc[0]-'A'), rune(0x1F1E6) + rune(cc[1]-'A')})"
if old not in s:
    raise SystemExit("flag marker not found")
p.write_text(s.replace(old, new, 1))
