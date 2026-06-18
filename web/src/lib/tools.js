export const toolHints = {
	ocrmypdf: {
		'Debian/Ubuntu': 'sudo apt install ocrmypdf',
		'Arch': 'sudo pacman -S ocrmypdf',
		'Fedora': 'sudo dnf install ocrmypdf',
		'macOS': 'brew install ocrmypdf',
		'pip': 'pip install ocrmypdf'
	},
	gs: {
		'Debian/Ubuntu': 'sudo apt install ghostscript',
		'Arch': 'sudo pacman -S ghostscript',
		'Fedora': 'sudo dnf install ghostscript',
		'macOS': 'brew install ghostscript'
	},
	pdftotext: {
		'Debian/Ubuntu': 'sudo apt install poppler-utils',
		'Arch': 'sudo pacman -S poppler',
		'Fedora': 'sudo dnf install poppler-utils',
		'macOS': 'brew install poppler'
	},
	curl: {
		'Debian/Ubuntu': 'sudo apt install curl',
		'Arch': 'sudo pacman -S curl',
		'Fedora': 'sudo dnf install curl',
		'macOS': 'brew install curl'
	}
};

export function hintsForEngine(engine) {
	return toolHints[engine] ?? {};
}
