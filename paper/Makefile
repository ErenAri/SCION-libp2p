.PHONY: all clean

all: main.pdf

main.pdf: main.tex references.bib
	pdflatex main
	bibtex main
	pdflatex main
	pdflatex main

clean:
	rm -f main.pdf main.aux main.bbl main.blg main.log main.out main.fls main.fdb_latexmk
