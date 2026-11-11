# General instructions for this project

## Communicate
* don't be overly enthusiastic, don't say things like "rock solid", "production ready", 'professional-grade', 'professional', 'high performing', "production-ready", "flawlessly", "Future-proof", etc ...

## General development
* run tests before making a change, fix any issues and then after making a change so code and tests are always in sync.
* any time a new command is introduced it should be documented in README.md or another internal document so it can be reproduced
* use ./claude_kill instead of kill command to kill processes
* the filter spec and `filterrules/testdata/filter_conformance.json` are targy's (`~/projects/targy`); the group files of `filters/` are copied by targy. A change to the conformance file or to a group file must be reported to targy (its `make groups-sync` copies the groups)

## Frontend code
* prefer TypeScript to JavaScript
* the platform to build upon is Bun for its native Type Script integration
* always ask before adding a dependency on an external javascript or typescript package/library
* avoid percentage values in CSS for data intensive areas because it's bad for performance as the browser needs to load all the data before doing the layout

## Python code
* use dataclass for classes
* use pyrefly to validate types of all new python code, always run it before/after making changes and make sure it's clean

