"""Bounded, diagnosable waits for tests that coordinate with asynchronous work.

A test that waits for a service, task or child process uses these helpers instead of choosing
its own deadline. Every budget comes from `timeout()`: a generous default, multiplied by
`KOINON_TEST_TIMEOUT_SCALE`, which CI raises on slower runners. A timeout names what the test
was waiting for and the last state it observed; a task or process that has already failed is
reported as that failure, not as a timeout.

The asynchronous helpers keep their own deadline on the event loop's clock. They never use
`asyncio.timeout` or `asyncio.wait_for`, which some tests patch while they run.
"""
import asyncio
import math
import os
import time

DEFAULT_SECONDS = 30
SCALE_VARIABLE = 'KOINON_TEST_TIMEOUT_SCALE'


def read_scale(value):
    """Return the timeout scale that `value` names; unset means 1."""
    if value is None or value == '':
        return 1.0
    try:
        scale = float(value)
    except ValueError:
        raise ValueError(f'{SCALE_VARIABLE} must be a finite number of at least 1, not {value!r}') from None
    if not (math.isfinite(scale) and scale >= 1):
        raise ValueError(f'{SCALE_VARIABLE} must be a finite number of at least 1, not {value!r}')
    return scale


SCALE = read_scale(os.environ.get(SCALE_VARIABLE))


def timeout(seconds=None):
    """The budget for one wait: `seconds`, or the default, times the scale."""
    return (DEFAULT_SECONDS if seconds is None else seconds) * SCALE


def expired(what, budget, observe):
    message = f'timed out after {budget:g}s waiting for {what}'
    if observe is not None:
        message += f'; last observed: {observe()!r}'
    return AssertionError(message)


async def wait_until(condition, what, *, seconds=None, task=None, observe=None, interval=.01):
    """Poll `condition()` until it is true and return its value.

    When `task` is done before the condition holds, it is awaited so that its exception
    propagates instead of a timeout.
    """
    budget = timeout(seconds)
    loop = asyncio.get_running_loop()
    deadline = loop.time() + budget
    while True:
        if task is not None and task.done():
            await task
        value = condition()
        if value:
            return value
        if loop.time() >= deadline:
            raise expired(what, budget, observe)
        await asyncio.sleep(interval)


def wait_until_sync(condition, what, *, seconds=None, process=None, observe=None, interval=.05):
    """Poll `condition()` until it is true and return its value, for blocking code.

    The condition is checked first, so a condition that is the process's own exit succeeds.
    Only when the condition is still false and `process` has exited does the wait fail early,
    with the exit status. The process's pipes are never read here; `observe` may report what
    the test itself captured.
    """
    budget = timeout(seconds)
    deadline = time.monotonic() + budget
    while True:
        value = condition()
        if value:
            return value
        if process is not None and process.poll() is not None:
            message = f'process exited with status {process.returncode} while waiting for {what}'
            if observe is not None:
                message += f'; last observed: {observe()!r}'
            raise AssertionError(message)
        if time.monotonic() >= deadline:
            raise expired(what, budget, observe)
        time.sleep(interval)


async def settle(awaitable, what, *, seconds=None):
    """Await one awaitable within the budget and return its result.

    On timeout the awaitable is cancelled, and the failure names `what`.
    """
    budget = timeout(seconds)
    future = asyncio.ensure_future(awaitable)
    done, _ = await asyncio.wait({future}, timeout=budget)
    if not done:
        future.cancel()
        raise expired(what, budget, None)
    return future.result()
