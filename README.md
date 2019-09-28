x32
===

Messing around with Behringer X32 OSC and Reaper in Golang.


Notes
-----

### Track colour/icon hints
Set trigger words and associated colours/icons in the config file.
Reaper sends a default "Track <num>" name if you leave the track name empty, 
but setting the track name in Reaper to whitespace only text will cause the
associted scribble pad to be turned black.

### Reaper/X32 track mapping
Set track mapping in config file.

### Reaper OSC config
You should configure an OSC devide in the Reaper preferences, use the provided
OSC config file: TODO

place the file in the reaper OSC config directory (...), and ...

If your network supports it (test and see!), try setting the max packet size to
50000 in the Reaper OSC configuration (TODO pic).




###


Thoughts
--------

Stuff to do:
 - proxy between x32 and Reaper OSC
		+ <st>Track mapping</st>
    + FX control mapping
    + <st>auto icon/colour on x32<<st>
    + button > /action mapping
    + ...
		+ bus sends (volume/pan)
 		+ detect reconnects and refresh

 
