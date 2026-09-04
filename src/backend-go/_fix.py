path = "internal/routers/messages_stream.go"
with open(path) as f:
    lines = f.readlines()

n = 208  # 0-based index for line 209
line = lines[n]
prefix = line[:line.index("sources")]
suffix = line[line.index("sources"):]
# We need 4 closing parens after "sources" (close formatSourcesForSSE,
# fmt.Sprintf, []byte, w.Write), then ';' then ' err != nil {'
new_suffix = "sources" + (4 * ")") + ";" + " err != nil {\n"
lines[n] = prefix + new_suffix
with open(path, "w") as f:
    f.writelines(lines)
# Verify
with open(path) as f:
    content = f.readlines()
print("Line 209:", repr(content[n]))
# Count the full range 207-209
with open(path) as f:
    all_lines = f.readlines()
range_text = "".join(all_lines[206:210])
print("opens 207-209:", range_text.count("("))
print("closes 207-209:", range_text.count(")"))
