package utils

// HideChildWindows makes HideWindow keep child processes from opening a
// console window. The desktop app sets it at startup; a console program
// leaves it off so its children share the terminal.
var HideChildWindows bool
