"""Validate the T03 account contract and its illustrative replay relationships.

These fixtures describe the contract; they are not runtime concurrency tests.
"""
import copy
import json
from pathlib import Path

from jsonschema import Draft202012Validator, FormatChecker
from openapi_spec_validator import validate

root = Path(__file__).resolve().parents[2]
document = json.loads((root / 'api/account.openapi.json').read_text())
validate(document)

def validator(schema):
    return Draft202012Validator(dict(schema, components=document['components']),
                                format_checker=FormatChecker())

def named(name):
    return validator({'$ref': '#/components/schemas/' + name})

count = 0

def visit(node):
    global count
    if isinstance(node, dict):
        if 'schema' in node and 'examples' in node:
            for example in node['examples'].values():
                validator(node['schema']).validate(example['value'])
                count += 1
        for child in node.values():
            visit(child)
    elif isinstance(node, list):
        for child in node:
            visit(child)

visit(document['paths'])
assert count > 0
for path in document['paths'].values():
    for method, operation in path.items():
        if method in ('get', 'post', 'patch', 'delete'):
            assert operation.get('security', document['security']) == [{'bearerAuth': []}]

negative = [
    ('AccountCreate', {'balance': 1}), ('AccountCreate', {'userId': 'caller'}),
    ('AccountCreate', {'currency': 'EUR'}), ('AccountCreate', {'status': 'disabled'}),
    ('AccountCreate', {'displayName': None}), ('AccountCreate', {'description': None}),
    ('AccountCreate', {'displayName': 'x'*101}), ('AccountCreate', {'description': 'x'*1001}),
    ('AccountCreate', {'description': 'a\0b'}), ('AccountUpdate', {}),
    ('AccountUpdate', {'displayName': 'a\0b'}), ('AccountUpdate', {'accountNumber': '123'}),
    ('AccountUpdate', {'balance': 0}), ('AccountUpdate', {'description': 1}),
]
for name, payload in negative:
    assert not named(name).is_valid(payload), (name, payload)
for schema in ('AccountCreate', 'AccountUpdate'):
    named(schema).validate({'displayName': '', 'description': ''})
    named(schema).validate({'displayName': '界'*100, 'description': '界'*1000})

account_path = document['paths']['/accounts/{accountNumber}']
def examples(operation):
    return {name: entry['value'] for name, entry in operation['responses']['200']['content']['application/json']['examples'].items()}

closures = examples(account_path['delete'])
original = closures['positiveBalanceClosure']
assert original == closures['identicalRetry'] == closures['historicalRetryAfterReactivation']
assert closures['newKeyWhileDisabled']['lifecycleRequestId'] != original['lifecycleRequestId']
assert closures['newKeyWhileDisabled']['closureWithdrawal'] == original['closureWithdrawal']
assert closures['zeroBalanceClosure']['closureWithdrawal'] is None
reactivations = examples(document['paths']['/accounts/{accountNumber}/reactivation']['post'])
assert reactivations['reactivated'] == reactivations['historicalRetryAfterClosure']
assert reactivations['alreadyActiveWithFunds']['account']['balance'] == 125
for change in (
    {'account': dict(original['account'], balance=1)},
    {'account': dict(original['account'], status='active')},
    {'closureWithdrawal': dict(original['closureWithdrawal'], amount=0)},
    {'closureWithdrawal': dict(original['closureWithdrawal'], balanceAfter=1)},
    {'closureWithdrawal': dict(original['closureWithdrawal'], amount=-2147483648)},
):
    assert not named('LifecycleResult').is_valid(dict(original, **change))
invalid_reactivation = copy.deepcopy(reactivations['reactivated'])
invalid_reactivation['closureWithdrawal'] = original['closureWithdrawal']
assert not named('LifecycleResult').is_valid(invalid_reactivation)
for operation in (account_path['delete'], document['paths']['/accounts/{accountNumber}/reactivation']['post']):
    key = operation['parameters'][0]
    assert key['name'] == 'Idempotency-Key' and key['required'] and key['in'] == 'header'
    assert not validator(key['schema']).is_valid('not-a-uuid')
    assert 'requestBody' not in operation
limit = document['paths']['/accounts']['get']['parameters'][0]['schema']
for value in (0, 101, 1.5, '50'):
    assert not validator(limit).is_valid(value)
assert limit['default'] == 50
for value in (1, 100):
    validator(limit).validate(value)
page = document['paths']['/accounts']['get']['responses']['200']['content']['application/json']['examples']['page']['value']
assert isinstance(page['items'][0]['accountNumber'], str)
assert page['items'][0]['accountNumber'].startswith('0')
print(f'Account OpenAPI and {count} examples passed; metadata, pagination and lifecycle/replay fixtures passed.')
