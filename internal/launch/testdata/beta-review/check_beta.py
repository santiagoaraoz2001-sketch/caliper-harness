from pathlib import Path
assert Path('beta.txt').read_bytes() == b'beta\n'
print('beta artifact PASS')
