"""Validate T04 schemas/examples; not a simulation of the future processor."""
import json
from pathlib import Path

from jsonschema import Draft202012Validator, FormatChecker
from openapi_spec_validator import validate

root = Path(__file__).resolve().parents[2]
doc = json.loads((root / 'api/operation.openapi.json').read_text())
validate(doc)
def check(schema):
    return Draft202012Validator(dict(schema, components=doc['components']), format_checker=FormatChecker())
def named(name):
    return check({'$ref': '#/components/schemas/' + name})
count = 0
def visit(node):
    global count
    if isinstance(node, dict):
        if 'schema' in node and 'examples' in node:
            for example in node['examples'].values():
                check(node['schema']).validate(example['value'])
                count += 1
        for value in node.values():
            visit(value)
    elif isinstance(node, list):
        for value in node:
            visit(value)
visit(doc['paths'])
post = doc['paths']['/accounts/{accountNumber}/transactions']['post']
get = doc['paths']['/operations/{operationId}']['get']
def examples(operation, status):
    return {key: entry['value'] for key, entry in operation['responses'][status]['content']['application/json']['examples'].items()}
pending = examples(post, '202')
terminal = examples(post, '200')
assert pending['accepted'] == pending['pendingReplay']
assert terminal['success'] == terminal['successReplayAfterLaterBalanceChange']
assert terminal['insufficientFunds'] == terminal['sameDeclineAfterFundsArrive']
assert terminal['closedWhilePending'] == terminal['sameClosureDeclineAfterReactivation']
assert '202' not in get['responses']
assert '422' not in post['responses'] and '422' not in get['responses']
assert 'Location' in post['responses']['202']['headers']
assert 'Location' in post['responses']['200']['headers']
for status, code in [('429','RATE_LIMITED'),('503','SERVICE_UNAVAILABLE')]:
    assert examples(post,status)[code]['code'] == code
    retry = post['responses'][status]['headers']['Retry-After']
    assert retry['required']
    check(retry['schema']).validate('5')
    assert not check(retry['schema']).is_valid('0')
for outcome in examples(get, '200').values():
    if outcome['status'] != 'pending':
        assert outcome['completedAt'] >= outcome['acceptedAt']
    if outcome['status'] == 'declined':
        assert 'transactionId' not in outcome and 'balanceAfter' not in outcome
        expected = 409 if outcome['error']['code']=='ACCOUNT_DISABLED' else 422
        assert outcome['error']['httpStatus']==expected
valid = {'operationId': pending['accepted']['operationId'], 'amount': 1}
invalid = [{}, {'operationId': valid['operationId']}, dict(valid,operationId='bad')]
invalid += [dict(valid,amount=a) for a in [0,-2147483648,2147483648,1.5,'250',None,True]]
invalid += [dict(valid,**{key: 'forbidden'}) for key in ['userId','source','currency','transactionId','status']]
for payload in invalid:
    assert not named('Submission').is_valid(payload), payload
for amount in [-2147483647,-1,1,2147483647]:
    named('Submission').validate(dict(valid,amount=amount))
# JSON Schema's integer is mathematical; lexical integer-only enforcement is
# additionally a transport requirement, to be tested in T24's real decoder.
for extra in ['transactionId','balanceAfter','completedAt','error']:
    assert not named('Operation').is_valid(dict(pending['accepted'],**{extra:None}))
for key in ['transactionId','balanceAfter','completedAt']:
    payload = dict(terminal['success']); del payload[key]
    assert not named('Operation').is_valid(payload)
for key, value in [('transactionId',valid['operationId']),('balanceAfter',0)]:
    assert not named('Operation').is_valid(dict(terminal['insufficientFunds'],**{key:value}))
for code, status in [('ACCOUNT_DISABLED',422),('INSUFFICIENT_FUNDS',409),('INTERNAL_ERROR',500)]:
    payload = dict(terminal['insufficientFunds'],error={'code':code,'message':'Example','httpStatus':status})
    assert not named('Operation').is_valid(payload)
for status in ['400','401','404','409','429','503']:
    for payload in examples(post,status).values():
        assert set(payload)=={'code','message'}
assert doc['security'] == [{'bearerAuth': []}]
print(f'Operation OpenAPI and {count} examples passed; amount, outcome, rejection and replay checks passed.')
