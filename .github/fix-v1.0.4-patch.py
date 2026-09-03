from pathlib import Path

p = Path('.github/patch-v1.0.4.py')
s = p.read_text()
s = s.replace('s, n = vpn_pattern.subn(vpn_new, s, count=1)', 's, n = vpn_pattern.subn(lambda m: vpn_new, s, count=1)', 1)
s = s.replace('s, n = proxio_pattern.subn(proxio_new, s, count=1)', 's, n = proxio_pattern.subn(lambda m: proxio_new, s, count=1)', 1)
p.write_text(s)
