#!/usr/bin/env python3
"""Turn the output of `gclient flatten` into a list for cipd-meta-mirror.

Reads the CIPD packages from the flattened DEPS, keeps those whose condition
holds for the given settings, expands ${{platform}} and prints one
"<package> <version>" line per package.

    gclient flatten > flat.deps
    scripts/deps2list.py flat.deps --platform linux-amd64 \
        --set checkout_linux=true --set checkout_android=false > packages.txt

The file is parsed with the ast module and never executed.
"""
import argparse
import ast
import re
import sys

MACRO = re.compile(r"\$\{\{([^}]*)\}\}")


def evaluate(node, env):
    """Evaluates the small subset of Python that DEPS files use."""
    if isinstance(node, ast.Constant):
        return node.value
    if isinstance(node, ast.Dict):
        return {evaluate(k, env): evaluate(v, env) for k, v in zip(node.keys, node.values)}
    if isinstance(node, (ast.List, ast.Tuple)):
        return [evaluate(e, env) for e in node.elts]
    if isinstance(node, ast.Name):
        if node.id in env:
            return env[node.id]
        raise KeyError(node.id)
    if isinstance(node, ast.Call) and isinstance(node.func, ast.Name) and node.func.id == "Var":
        if len(node.args) != 1 or node.keywords:
            raise ValueError("unsupported Var() call")
        return env[evaluate(node.args[0], env)]
    if isinstance(node, ast.BinOp) and isinstance(node.op, ast.Add):
        return evaluate(node.left, env) + evaluate(node.right, env)
    if isinstance(node, ast.BoolOp):
        values = (evaluate(v, env) for v in node.values)
        return all(values) if isinstance(node.op, ast.And) else any(values)
    if isinstance(node, ast.UnaryOp) and isinstance(node.op, ast.Not):
        return not evaluate(node.operand, env)
    if isinstance(node, ast.Compare) and len(node.ops) == 1:
        left, right = evaluate(node.left, env), evaluate(node.comparators[0], env)
        op = node.ops[0]
        if isinstance(op, ast.Eq):
            return left == right
        if isinstance(op, ast.NotEq):
            return left != right
        if isinstance(op, ast.In):
            return left in right
        if isinstance(op, ast.NotIn):
            return left not in right
    raise ValueError("unsupported expression: " + ast.dump(node)[:80])


def condition(expr, env):
    return bool(evaluate(ast.parse(expr, mode="eval").body, env))


def expand(name, platforms):
    macros = set(MACRO.findall(name))
    if not macros:
        return [name]
    out = []
    for platform in platforms:
        os_name, _, arch = platform.partition("-")
        values = {"platform": platform, "os": os_name, "arch": arch}
        unknown = macros - values.keys()
        if unknown:
            raise ValueError("unsupported macro %s in %s" % (sorted(unknown), name))
        out.append(MACRO.sub(lambda m: values[m.group(1)], name))
    return out


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("deps", help="file produced by `gclient flatten`")
    ap.add_argument("--platform", action="append", required=True,
                    help="CIPD platform to expand ${{platform}} to, e.g. linux-amd64 (repeatable)")
    ap.add_argument("--set", action="append", default=[], metavar="NAME=VALUE",
                    help="override a variable, e.g. checkout_android=true (repeatable)")
    args = ap.parse_args()

    with open(args.deps) as f:
        tree = ast.parse(f.read())
    assigned = {}
    for stmt in tree.body:
        if isinstance(stmt, ast.Assign) and len(stmt.targets) == 1 and isinstance(stmt.targets[0], ast.Name):
            assigned[stmt.targets[0].id] = stmt.value

    env = {}
    if "vars" in assigned:
        # Variables may refer to each other through Var(); resolve lazily.
        raw = {}
        for k, v in zip(assigned["vars"].keys, assigned["vars"].values):
            raw[evaluate(k, {})] = v
        for _ in range(len(raw) + 1):
            for k, v in raw.items():
                try:
                    env[k] = evaluate(v, env)
                except KeyError:
                    pass
    for item in args.set:
        name, _, value = item.partition("=")
        env[name] = {"true": True, "false": False}.get(value.lower(), value)

    if "deps" not in assigned:
        sys.exit("no deps = {...} in " + args.deps)
    try:
        deps = evaluate(assigned["deps"], env)
    except KeyError as e:
        sys.exit("unknown variable %s: pass it with --set %s=..." % (e, e.args[0]))

    lines, skipped = set(), 0
    for path, dep in deps.items():
        if not isinstance(dep, dict) or dep.get("dep_type") != "cipd":
            continue
        cond = dep.get("condition")
        if cond:
            try:
                if not condition(cond, env):
                    skipped += 1
                    continue
            except KeyError as e:
                sys.exit("%s: condition %r uses unknown variable %s: pass --set %s=..." % (path, cond, e, e.args[0]))
        for pkg in dep.get("packages", []):
            for name in expand(pkg["package"], args.platform):
                lines.add("%s %s" % (name, pkg["version"]))

    for line in sorted(lines):
        print(line)
    sys.stderr.write("%d packages listed, %d cipd dependencies skipped by condition\n" % (len(lines), skipped))


if __name__ == "__main__":
    main()
