"""Generation-bound private shutdown requests; legacy servers must refuse the opcode."""
import asyncio

import platform_support

from inbox_schema import hex_value
from peer_transport import control_exchange

CAPABILITY = 'generation_bound_stop'
OPERATION = 'stop-generation'
CODES = frozenset(('invalid_request', 'not_this_instance',
                   'guarded_stop_unsupported', 'guarded_stop_unconfirmed'))


class StopError(ValueError):
    def __init__(self, code):
        if code not in CODES:
            raise ValueError('unknown guarded stop error')
        self.code = code
        super().__init__(code)


def validate(request, generation):
    if (not isinstance(request, dict) or set(request) != {'op', 'protocol', 'generation'}
            or request['op'] != OPERATION or type(request['protocol']) is not int
            or request['protocol'] != 1 or not hex_value(request['generation'], 32)):
        raise StopError('invalid_request')
    if request['generation'] != generation:
        raise StopError('not_this_instance')


async def request_stop(root, generation, pid, proc_start, *, timeout=10):
    """Request one captured instance's stop; success does not establish process exit.

    Capability, generation and the captured PID/start marker must agree before sending. A distinct
    operation also refuses on an old replacement between the two connections.
    There is no retry or fallback to an unguarded request after any uncertainty.
    """
    if (not hex_value(generation, 32) or type(pid) is not int or pid <= 0
            or not isinstance(proc_start, str) or not 0 < len(proc_start) <= 128
            or any(ord(char) < 32 or ord(char) == 127 for char in proc_start)):
        raise StopError('invalid_request')
    async def verify_process():
        if await asyncio.to_thread(platform_support.process_state, pid, proc_start) != 'alive':
            raise StopError('not_this_instance')
    async with asyncio.timeout(timeout):
        await verify_process()
        reply, peer_pid = await control_exchange(root, dict(op='status'), timeout=timeout)
        value = reply.get('result')
        if (reply.get('ok') is not True or not isinstance(value, dict)
                or type(peer_pid) is not int or peer_pid != pid
                or type(value.get('pid')) is not int or value['pid'] != pid
                or value.get('generation') != generation):
            raise StopError('not_this_instance')
        capabilities = value.get('control_capabilities')
        if not isinstance(capabilities, list) or CAPABILITY not in capabilities:
            raise StopError('guarded_stop_unsupported')
        await verify_process()
        reply, peer_pid = await control_exchange(
            root, dict(op=OPERATION, protocol=1, generation=generation), timeout=timeout)
        result = reply.get('result')
        if (type(peer_pid) is not int or peer_pid != pid or reply.get('ok') is not True
                or not isinstance(result, dict)
                or set(result) != {'stopping', 'generation', 'protocol'}
                or result['stopping'] is not True or type(result['protocol']) is not int
                or result['protocol'] != 1 or result['generation'] != generation):
            raise StopError('guarded_stop_unconfirmed')
        return reply['result']
