import json,runpy
from pathlib import Path
runpy.run_path('checks/check_beta.py')
assert json.loads(Path('checks/review_gate.json').read_text())['approved'] is True, 'declared reviewer-only approval is false'
print('beta reviewer gate PASS')
