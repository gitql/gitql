<img src="https://rawgit.com/gitql/gitql/master/gitql-logo.svg" alt="gitql" width="250"/> 

image:https://travis-ci.org/gitql/gitql.svg?branch=master[Build Status,link=https://travis-ci.org/gitql/gitql] image:https://codecov.io/gh/gitql/gitql/branch/master/graph/badge.svg[codecov,link=https://codecov.io/gh/gitql/gitql] image:https://godoc.org/github.com/gitql/gitql?status.svg[GoDoc,link=https://godoc.org/github.com/gitql/gitql]

**gitql** is a SQL interface to Git repositories, written in Go.

**WARNING: gitql is still in a very early stage of development. It is considered experimental.**

## Installation

Check the [Releases](https://github.com/gitql/gitql/releases) page to download
the gitql binary.

## Usage

```
Usage:
  gitql [OPTIONS] <query | version>

Help Options:
  -h, --help  Show this help message

Available commands:
  query    Execute a SQL query a repository.
  version  Show the version information.
```

For example:

```bash
$ cd my_git_repo
$ gitql query 'SELECT hash, author_email, author_name FROM commits LIMIT 2;' 
SELECT hash, author_email, author_name FROM commits LIMIT 2;
+------------------------------------------+--------------------+---------------+
|                   HASH                   |   AUTHOR EMAIL     |  AUTHOR NAME  |
+------------------------------------------+--------------------+---------------+
| 02e0aa0ef807d2ae4d02ecdbe37681db9e812544 | Santiago M. Mola   | user1@test.io |
| 034cb63f77f4a0d30d26dabb999d348be6640df7 | Antonio J. Navarro | user2@test.io |
+------------------------------------------+--------------------+---------------+
```

## Tables

*TODO*

## SQL syntax

gitql supports a subset of the SQL standard, currently including:

* `SELECT`
* `WHERE`
* `ORDER BY` (with `ASC` and `DESC`)
* `LIMIT`

## License

gitql is licensed under the
https://github.com/gitql/gitql/blob/master/LICENSE[MIT License].